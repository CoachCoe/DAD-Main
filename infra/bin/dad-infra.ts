#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { getConfig } from '../lib/config';

/**
 * DAD infrastructure CDK app entrypoint.
 *
 * Environment is selected with `-c env=staging|production` (default staging).
 * Account/region come from the resolved EnvConfig, with CDK_DEFAULT_ACCOUNT /
 * CDK_DEFAULT_REGION as the offline-synth fallback so `cdk synth` works with
 * dummy credentials.
 *
 * FEAT-001 establishes build/test/synth tooling only — no AWS resources yet.
 * FEAT-002 fills in the stack instantiation (NetworkStack, DataStack, etc.).
 */
const app = new cdk.App();

const envName = (app.node.tryGetContext('env') as string | undefined) ?? 'staging';
const config = getConfig(envName);

// Resolve the deployment target. Prefer the config account/region; fall back to
// the CDK default env vars so offline synth succeeds without real credentials.
const env: cdk.Environment = {
  account: config.account ?? process.env.CDK_DEFAULT_ACCOUNT,
  region: config.region ?? process.env.CDK_DEFAULT_REGION,
};

// Tag everything with the environment for cost allocation and traceability.
cdk.Tags.of(app).add('Project', 'dad');
cdk.Tags.of(app).add('Environment', config.env);

// TODO(FEAT-002+): replace this placeholder with the real stacks, e.g.
//   const network = new NetworkStack(app, `dad-network-${config.env}`, { env, config });
//   new DataStack(app, `dad-data-${config.env}`, { env, config, vpc: network.vpc, ... });
// For FEAT-001 the app has no AWS resources yet, but the CDK CLI errors out with
// "This app contains no stacks", so we synth a single empty placeholder stack so
// `cdk synth` succeeds offline. FEAT-002 removes this and adds the real stacks.
new cdk.Stack(app, `dad-foundation-${config.env}`, {
  env,
  description: `DAD ${config.env} — CDK foundation placeholder (no resources yet; replaced in FEAT-002).`,
});

app.synth();
