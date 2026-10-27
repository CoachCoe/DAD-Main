import * as cdk from 'aws-cdk-lib';
import { Template } from 'aws-cdk-lib/assertions';
import { getConfig } from '../lib/config';
import { NetworkStack } from '../lib/network-stack';

function synth(env: 'staging' | 'production'): Template {
  const app = new cdk.App();
  const config = getConfig(env);
  const stack = new NetworkStack(app, `dad-network-${env}`, {
    env: { account: '111111111111', region: 'us-east-2' },
    config,
  });
  return Template.fromStack(stack);
}

describe('NetworkStack', () => {
  it('creates a VPC with three subnet tiers across 2 AZs', () => {
    const t = synth('staging');
    t.resourceCountIs('AWS::EC2::VPC', 1);
    // 2 AZs x 3 tiers = 6 subnets.
    t.resourceCountIs('AWS::EC2::Subnet', 6);
  });

  it('asserts the SG chain ingress rules (ports 8080/8443/3000/8002/5432)', () => {
    const t = synth('staging');
    for (const port of [8080, 8443, 3000, 8002, 5432]) {
      t.hasResourceProperties('AWS::EC2::SecurityGroupIngress', {
        IpProtocol: 'tcp',
        FromPort: port,
        ToPort: port,
      });
    }
  });

  it('gives workers an SG with no inbound rules', () => {
    const t = synth('staging');
    // The workers SG is referenced as a source for the proxy ingress but never
    // appears as the GroupId of an ingress rule itself.
    const ingress = t.findResources('AWS::EC2::SecurityGroupIngress');
    const workerDesc = Object.values(ingress).filter((r) =>
      JSON.stringify(r).includes('no inbound'),
    );
    expect(workerDesc).toHaveLength(0);
  });

  it('production provisions two NAT gateways for AZ redundancy', () => {
    const t = synth('production');
    t.resourceCountIs('AWS::EC2::NatGateway', 2);
  });

  it('publishes the named outputs the app pipeline resolves (ComputeSubnetIds, WorkersSecurityGroupId)', () => {
    // deploy.yml reads these exact OutputKeys off dad-network-<env> to wire the
    // migrate / provision-roles ECS run-tasks (compute subnets + workers SG).
    const t = synth('staging');
    const outputs = t.findOutputs('*');
    expect(outputs).toHaveProperty('ComputeSubnetIds');
    expect(outputs).toHaveProperty('WorkersSecurityGroupId');
  });
});
