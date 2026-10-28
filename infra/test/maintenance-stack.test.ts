import { Template, Match } from 'aws-cdk-lib/assertions';
import { buildGraph } from './helpers';

describe('MaintenanceStack', () => {
  it('defines 6 task definitions: 5 maintenance + reconcile', () => {
    const t = Template.fromStack(buildGraph('staging').maintenance);
    // migrate, provision-roles, staff, rekey, monitor, reconcile.
    t.resourceCountIs('AWS::ECS::TaskDefinition', 6);
  });

  it('defines the exact maintenance commands', () => {
    const t = Template.fromStack(buildGraph('staging').maintenance);
    for (const cmd of [
      ['migrate'],
      ['provision'],
      ['staff'],
      ['rekey'],
      ['monitor'],
      ['reconcile'],
    ]) {
      t.hasResourceProperties('AWS::ECS::TaskDefinition', {
        ContainerDefinitions: Match.arrayWith([
          Match.objectLike({ Command: cmd }),
        ]),
      });
    }
  });

  it('schedules reconcile at ~00:30 America/New_York', () => {
    const t = Template.fromStack(buildGraph('staging').maintenance);
    t.hasResourceProperties('AWS::Scheduler::Schedule', {
      ScheduleExpression: 'cron(30 0 * * ? *)',
      ScheduleExpressionTimezone: 'America/New_York',
    });
  });

  it('alarms on a non-zero reconcile exit and notifies SNS', () => {
    const t = Template.fromStack(buildGraph('staging').maintenance);
    t.resourceCountIs('AWS::SNS::Topic', 1);
    t.hasResourceProperties('AWS::CloudWatch::Alarm', {
      AlarmName: 'dad-staging-reconcile-nonzero-exit',
      ComparisonOperator: 'GreaterThanOrEqualToThreshold',
      AlarmActions: Match.anyValue(),
    });
  });
});
