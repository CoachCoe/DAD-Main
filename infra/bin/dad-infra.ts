#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { getConfig } from '../lib/config';
import { NetworkStack } from '../lib/network-stack';
import { DataStack } from '../lib/data-stack';
import { EcsClusterStack } from '../lib/ecs-cluster-stack';
import { ServicesStack } from '../lib/services-stack';
import { MaintenanceStack } from '../lib/maintenance-stack';
import { EdgeStack } from '../lib/edge-stack';
import { ObservabilityStack } from '../lib/observability-stack';
import { SesStack } from '../lib/ses-stack';
import { PipelineIamStack } from '../lib/pipeline-iam-stack';

/**
 * DAD infrastructure CDK app entrypoint.
 *
 * Environment is selected with `-c env=staging|production` (default staging).
 * Account/region come from the resolved EnvConfig, with CDK_DEFAULT_ACCOUNT /
 * CDK_DEFAULT_REGION as the offline-synth fallback so `cdk synth` works with
 * dummy credentials.
 *
 * Stack graph (cross-references passed as props):
 *   network → data → ecs-cluster → services → {maintenance, edge, observability}
 *   ses and pipeline-iam are independent.
 */
const app = new cdk.App();

const envName = (app.node.tryGetContext('env') as string | undefined) ?? 'staging';
const config = getConfig(envName);

// Resolve the deployment target.
//
// For a real deploy, the account/region come from the resolved EnvConfig. For
// offline synth there are no credentials for the real account, and several
// constructs (VPC AZ selection, NLB) would otherwise trigger context lookups
// that require AWS calls. CDK signals offline synth by exporting dummy
// CDK_DEFAULT_ACCOUNT/CDK_DEFAULT_REGION, so we prefer those when present and
// fall back to the config account/region otherwise. The per-env config account
// still governs the deploy target in CI, where these env vars carry the real
// account id for the selected environment.
const env: cdk.Environment = {
  account: process.env.CDK_DEFAULT_ACCOUNT ?? config.account,
  region: process.env.CDK_DEFAULT_REGION ?? config.region,
};

// Tag everything with the environment for cost allocation and traceability.
cdk.Tags.of(app).add('Project', 'dad');
cdk.Tags.of(app).add('Environment', config.env);

const stackId = (name: string): string => `dad-${name}-${config.env}`;

// 1. Network — VPC, subnets, SG chain.
const network = new NetworkStack(app, stackId('network'), { env, config });

// 2. Data — RDS + RDS Proxy + per-login secrets.
const data = new DataStack(app, stackId('data'), {
  env,
  config,
  vpc: network.vpc,
  isolatedSubnets: network.isolatedSubnets,
  proxySecurityGroup: network.proxySecurityGroup,
});

// 3. ECS cluster — Fargate cluster, Service Connect namespace, ECR, log groups.
const ecsCluster = new EcsClusterStack(app, stackId('ecs-cluster'), {
  env,
  config,
  vpc: network.vpc,
});

// 4. Services — the nine long-running Fargate services.
const services = new ServicesStack(app, stackId('services'), {
  env,
  config,
  vpc: network.vpc,
  computeSubnets: network.computeSubnets,
  cluster: ecsCluster.cluster,
  namespaceName: ecsCluster.namespaceName,
  repositories: ecsCluster.repositories,
  logGroups: ecsCluster.logGroups,
  proxyEndpoint: data.proxyEndpoint,
  loginSecrets: data.loginSecrets,
  computeSubnetCidrs: network.computeSubnetCidrs,
  nginxSecurityGroup: network.nginxSecurityGroup,
  webSecurityGroup: network.webSecurityGroup,
  apiSecurityGroup: network.apiSecurityGroup,
  workersSecurityGroup: network.workersSecurityGroup,
});

// 5. Maintenance — one-off task defs + scheduled reconcile + exit alarm.
new MaintenanceStack(app, stackId('maintenance'), {
  env,
  config,
  vpc: network.vpc,
  computeSubnets: network.computeSubnets,
  cluster: ecsCluster.cluster,
  repositories: ecsCluster.repositories,
  proxyEndpoint: data.proxyEndpoint,
  loginSecrets: data.loginSecrets,
  workersSecurityGroup: network.workersSecurityGroup,
});

// 6. Edge — internet-facing NLB + Cloudflare SG toggle.
new EdgeStack(app, stackId('edge'), {
  env,
  config,
  vpc: network.vpc,
  publicSubnets: network.publicSubnets,
  nginxService: services.services.nginx,
  nginxSecurityGroup: network.nginxSecurityGroup,
  nlbSecurityGroup: network.nlbSecurityGroup,
});

// 7. Observability — SNS topic + ECS health + monitor queue-age alarms.
new ObservabilityStack(app, stackId('observability'), {
  env,
  config,
  clusterName: ecsCluster.cluster.clusterName,
  services: services.services,
});

// 8. SES — sending-subdomain identity + SMTP credentials.
new SesStack(app, stackId('ses'), { env, config });

// 9. Pipeline IAM — GitHub OIDC provider + deploy roles.
new PipelineIamStack(app, stackId('pipeline-iam'), { env, config });

app.synth();
