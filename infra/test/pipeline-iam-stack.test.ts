import { Template, Match } from 'aws-cdk-lib/assertions';
import { buildGraph } from './helpers';

describe('PipelineIamStack', () => {
  it('creates the GitHub OIDC provider', () => {
    const t = Template.fromStack(buildGraph('staging').pipelineIam);
    t.hasResourceProperties('Custom::AWSCDKOpenIdConnectProvider', {
      Url: 'https://token.actions.githubusercontent.com',
      ClientIDList: ['sts.amazonaws.com'],
    });
  });

  it('creates two deploy roles (infra + app) scoped to the repo/branch', () => {
    const t = Template.fromStack(buildGraph('staging').pipelineIam);
    for (const name of ['dad-staging-infra-deploy', 'dad-staging-app-deploy']) {
      t.hasResourceProperties('AWS::IAM::Role', { RoleName: name });
    }
    t.hasResourceProperties('AWS::IAM::Role', {
      AssumeRolePolicyDocument: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Condition: Match.objectLike({
              StringLike: {
                'token.actions.githubusercontent.com:sub': [
                  'repo:dynamicaid/dad:ref:refs/heads/dev',
                ],
              },
            }),
          }),
        ]),
      },
    });
  });

  it('production scopes the sub to the production GitHub Environment', () => {
    const t = Template.fromStack(buildGraph('production').pipelineIam);
    t.hasResourceProperties('AWS::IAM::Role', {
      AssumeRolePolicyDocument: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Condition: Match.objectLike({
              StringLike: {
                'token.actions.githubusercontent.com:sub': Match.arrayWith([
                  'repo:dynamicaid/dad:environment:production',
                ]),
              },
            }),
          }),
        ]),
      },
    });
  });
});
