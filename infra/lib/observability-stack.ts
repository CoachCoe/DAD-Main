import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ecs from 'aws-cdk-lib/aws-ecs';
import * as sns from 'aws-cdk-lib/aws-sns';
import * as snsSubs from 'aws-cdk-lib/aws-sns-subscriptions';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as cwActions from 'aws-cdk-lib/aws-cloudwatch-actions';
import { EnvConfig } from './config';
import { ServiceName } from './ecs-cluster-stack';

/** Props for {@link ObservabilityStack}. */
export interface ObservabilityStackProps extends cdk.StackProps {
  readonly config: EnvConfig;
  readonly clusterName: string;
  /** The nine long-running services keyed by service name. */
  readonly services: Record<ServiceName, ecs.FargateService>;
  /**
   * Optional email address to subscribe to the SNS topic. Comes from CDK
   * context `alarmEmail` if not supplied; left unsubscribed when absent.
   */
  readonly alarmEmail?: string;
}

/**
 * ObservabilityStack (PLAN item 10a).
 *
 * One SNS topic (optional email subscription), a per-service alarm for ECS
 * health (running task count below desired), and a monitor queue-age
 * placeholder alarm. All alarms notify the SNS topic.
 */
export class ObservabilityStack extends cdk.Stack {
  public readonly topic: sns.Topic;
  public readonly serviceHealthAlarms: cloudwatch.Alarm[] = [];
  public readonly monitorQueueAgeAlarm: cloudwatch.Alarm;

  constructor(scope: Construct, id: string, props: ObservabilityStackProps) {
    super(scope, id, props);

    const { config, clusterName, services } = props;

    this.topic = new sns.Topic(this, 'Alarms', {
      topicName: `dad-${config.env}-alarms`,
      displayName: `DAD ${config.env} alarms`,
    });

    const email =
      props.alarmEmail ??
      (this.node.tryGetContext('alarmEmail') as string | undefined);
    if (email) {
      this.topic.addSubscription(new snsSubs.EmailSubscription(email));
    }

    const action = new cwActions.SnsAction(this.topic);

    // --- ECS service health: running < desired --------------------------
    for (const [name, service] of Object.entries(services) as Array<
      [ServiceName, ecs.FargateService]
    >) {
      const running = new cloudwatch.Metric({
        namespace: 'ECS/ContainerInsights',
        metricName: 'RunningTaskCount',
        dimensionsMap: {
          ClusterName: clusterName,
          ServiceName: service.serviceName,
        },
        statistic: 'Minimum',
        period: cdk.Duration.minutes(1),
      });

      const desired =
        name === 'api'
          ? config.desiredCounts.api
          : name === 'web'
            ? config.desiredCounts.web
            : name === 'nginx'
              ? config.desiredCounts.nginx
              : config.desiredCounts.workers;

      const alarm = new cloudwatch.Alarm(this, `Health-${name}`, {
        alarmName: `dad-${config.env}-${name}-unhealthy`,
        alarmDescription: `ECS service ${name} has fewer running tasks than desired (${desired}).`,
        metric: running,
        threshold: desired,
        evaluationPeriods: 3,
        comparisonOperator:
          cloudwatch.ComparisonOperator.LESS_THAN_THRESHOLD,
        treatMissingData: cloudwatch.TreatMissingData.BREACHING,
      });
      alarm.addAlarmAction(action);
      this.serviceHealthAlarms.push(alarm);
    }

    // --- monitor queue-age placeholder ----------------------------------
    // The `monitor` task publishes a queue-age metric; until that metric name
    // is finalized this is a placeholder alarm on a custom metric so the wiring
    // and SNS action exist. Tune the metric/threshold when monitor lands.
    this.monitorQueueAgeAlarm = new cloudwatch.Alarm(
      this,
      'MonitorQueueAge',
      {
        alarmName: `dad-${config.env}-monitor-queue-age`,
        alarmDescription:
          'Placeholder: oldest unprocessed queue item age exceeded threshold.',
        metric: new cloudwatch.Metric({
          namespace: `DAD/${config.env}/Monitor`,
          metricName: 'QueueAgeSeconds',
          statistic: 'Maximum',
          period: cdk.Duration.minutes(5),
        }),
        threshold: 900,
        evaluationPeriods: 1,
        comparisonOperator:
          cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
        treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
      },
    );
    this.monitorQueueAgeAlarm.addAlarmAction(action);

    new cdk.CfnOutput(this, 'AlarmTopicArn', { value: this.topic.topicArn });
  }
}
