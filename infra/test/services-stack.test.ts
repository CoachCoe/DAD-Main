import { Template, Match } from 'aws-cdk-lib/assertions';
import { buildGraph } from './helpers';

describe('ServicesStack', () => {
  it('creates exactly 9 ECS services', () => {
    const t = Template.fromStack(buildGraph('staging').services);
    t.resourceCountIs('AWS::ECS::Service', 9);
  });

  it('defines the six worker commands as `worker --kind <kind>`', () => {
    const t = Template.fromStack(buildGraph('staging').services);
    const expected = [
      'stripe',
      'projections',
      'login-email',
      'email-changes',
      'messages',
      'push',
    ];
    for (const kind of expected) {
      t.hasResourceProperties('AWS::ECS::TaskDefinition', {
        ContainerDefinitions: Match.arrayWith([
          Match.objectLike({
            Command: ['worker', '--kind', kind],
          }),
        ]),
      });
    }
  });

  it('exposes container ports 8002 (api), 3000 (web), 8443 (nginx)', () => {
    const t = Template.fromStack(buildGraph('staging').services);
    for (const port of [8002, 3000, 8443]) {
      t.hasResourceProperties('AWS::ECS::TaskDefinition', {
        ContainerDefinitions: Match.arrayWith([
          Match.objectLike({
            PortMappings: Match.arrayWith([
              Match.objectLike({ ContainerPort: port }),
            ]),
          }),
        ]),
      });
    }
  });

  it('sets DONOR_API_BASE_URL=http://api:8002 on the web service', () => {
    const t = Template.fromStack(buildGraph('staging').services);
    t.hasResourceProperties('AWS::ECS::TaskDefinition', {
      ContainerDefinitions: Match.arrayWith([
        Match.objectLike({
          Environment: Match.arrayWith([
            {
              Name: 'DONOR_API_BASE_URL',
              Value: 'http://api:8002',
            },
          ]),
        }),
      ]),
    });
  });

  it('api registers in Service Connect reachable as api:8002', () => {
    const t = Template.fromStack(buildGraph('staging').services);
    t.hasResourceProperties('AWS::ECS::Service', {
      ServiceName: 'api',
      ServiceConnectConfiguration: Match.objectLike({
        Enabled: true,
        Namespace: 'dad.staging.local',
        Services: Match.arrayWith([
          Match.objectLike({
            PortName: 'api',
            ClientAliases: Match.arrayWith([
              Match.objectLike({ DnsName: 'api', Port: 8002 }),
            ]),
          }),
        ]),
      }),
    });
  });
});
