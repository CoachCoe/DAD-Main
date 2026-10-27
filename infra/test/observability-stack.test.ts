import { Template, Match } from 'aws-cdk-lib/assertions';
import { buildGraph } from './helpers';

describe('ObservabilityStack', () => {
  it('creates one SNS topic', () => {
    const t = Template.fromStack(buildGraph('staging').observability);
    t.resourceCountIs('AWS::SNS::Topic', 1);
  });

  it('creates a health alarm per service plus the monitor queue-age alarm', () => {
    const t = Template.fromStack(buildGraph('staging').observability);
    // 9 service-health alarms + 1 monitor queue-age placeholder.
    t.resourceCountIs('AWS::CloudWatch::Alarm', 10);
  });

  it('service-health alarms fire when running < desired', () => {
    const t = Template.fromStack(buildGraph('staging').observability);
    t.hasResourceProperties('AWS::CloudWatch::Alarm', {
      ComparisonOperator: 'LessThanThreshold',
      MetricName: 'RunningTaskCount',
      AlarmActions: Match.anyValue(),
    });
  });
});
