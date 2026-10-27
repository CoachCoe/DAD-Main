import * as cdk from 'aws-cdk-lib';
import { getConfig, EnvName } from '../lib/config';
import { NetworkStack } from '../lib/network-stack';
import { DataStack } from '../lib/data-stack';
import { EcsClusterStack } from '../lib/ecs-cluster-stack';
import { ServicesStack } from '../lib/services-stack';
import { MaintenanceStack } from '../lib/maintenance-stack';
import { EdgeStack } from '../lib/edge-stack';
import { ObservabilityStack } from '../lib/observability-stack';
import { SesStack } from '../lib/ses-stack';
import { PipelineIamStack } from '../lib/pipeline-iam-stack';

const ENV = { account: '111111111111', region: 'us-east-2' };

/** Build the full stack graph for an env, returning each stack for assertions. */
export function buildGraph(name: EnvName = 'staging') {
  const app = new cdk.App();
  const config = getConfig(name);

  const network = new NetworkStack(app, `dad-network-${name}`, {
    env: ENV,
    config,
  });
  const data = new DataStack(app, `dad-data-${name}`, {
    env: ENV,
    config,
    vpc: network.vpc,
    isolatedSubnets: network.isolatedSubnets,
    proxySecurityGroup: network.proxySecurityGroup,
  });
  const ecsCluster = new EcsClusterStack(app, `dad-ecs-cluster-${name}`, {
    env: ENV,
    config,
    vpc: network.vpc,
  });
  const services = new ServicesStack(app, `dad-services-${name}`, {
    env: ENV,
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
  const maintenance = new MaintenanceStack(app, `dad-maintenance-${name}`, {
    env: ENV,
    config,
    vpc: network.vpc,
    computeSubnets: network.computeSubnets,
    cluster: ecsCluster.cluster,
    repositories: ecsCluster.repositories,
    proxyEndpoint: data.proxyEndpoint,
    loginSecrets: data.loginSecrets,
    workersSecurityGroup: network.workersSecurityGroup,
  });
  const edge = new EdgeStack(app, `dad-edge-${name}`, {
    env: ENV,
    config,
    vpc: network.vpc,
    publicSubnets: network.publicSubnets,
    nginxService: services.services.nginx,
    nginxSecurityGroup: network.nginxSecurityGroup,
    nlbSecurityGroup: network.nlbSecurityGroup,
  });
  const observability = new ObservabilityStack(
    app,
    `dad-observability-${name}`,
    {
      env: ENV,
      config,
      clusterName: ecsCluster.cluster.clusterName,
      services: services.services,
    },
  );
  const ses = new SesStack(app, `dad-ses-${name}`, { env: ENV, config });
  const pipelineIam = new PipelineIamStack(app, `dad-pipeline-iam-${name}`, {
    env: ENV,
    config,
  });

  return {
    app,
    config,
    network,
    data,
    ecsCluster,
    services,
    maintenance,
    edge,
    observability,
    ses,
    pipelineIam,
  };
}
