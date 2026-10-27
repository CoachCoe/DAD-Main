import { Template, Match } from 'aws-cdk-lib/assertions';
import { buildGraph } from './helpers';

describe('DataStack', () => {
  it('runs PostgreSQL engine version 16', () => {
    const t = Template.fromStack(buildGraph('staging').data);
    t.hasResourceProperties('AWS::RDS::DBInstance', {
      Engine: 'postgres',
      EngineVersion: Match.stringLikeRegexp('^16'),
      StorageEncrypted: true,
    });
  });

  it('creates an RDS Proxy', () => {
    const t = Template.fromStack(buildGraph('staging').data);
    t.resourceCountIs('AWS::RDS::DBProxy', 1);
  });

  it('mints 13 secrets: 11 process logins + owner + master', () => {
    const t = Template.fromStack(buildGraph('staging').data);
    t.resourceCountIs('AWS::SecretsManager::Secret', 13);
  });

  it('multiAz is false for staging and true for production', () => {
    const staging = Template.fromStack(buildGraph('staging').data);
    staging.hasResourceProperties('AWS::RDS::DBInstance', { MultiAZ: false });

    const prod = Template.fromStack(buildGraph('production').data);
    prod.hasResourceProperties('AWS::RDS::DBInstance', { MultiAZ: true });
  });

  it('creates no Postgres roles (no custom resource for role creation)', () => {
    const t = Template.fromStack(buildGraph('staging').data);
    // The only resources are RDS, proxy, secrets, and their plumbing — never a
    // provisioning Lambda/custom resource that would create roles.
    const customResources = t.findResources('Custom::AWS');
    expect(Object.keys(customResources)).toHaveLength(0);
  });
});
