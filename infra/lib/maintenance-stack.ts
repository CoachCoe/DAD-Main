import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as ecs from 'aws-cdk-lib/aws-ecs';
import * as ecr from 'aws-cdk-lib/aws-ecr';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import * as sns from 'aws-cdk-lib/aws-sns';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as cwActions from 'aws-cdk-lib/aws-cloudwatch-actions';
import * as scheduler from 'aws-cdk-lib/aws-scheduler';
import * as iam from 'aws-cdk-lib/aws-iam';
import { EnvConfig } from './config';
import {
  buildDatabaseUrl,
  PROCESS_LOGINS,
  OWNER_LOGIN,
  ProcessLogin,
} from './db-url';
import { TaskFactory } from './task-factory';

/** Props for {@link MaintenanceStack}. */
export interface MaintenanceStackProps extends cdk.StackProps {
  readonly config: EnvConfig;
  readonly vpc: ec2.IVpc;
  readonly computeSubnets: ec2.SubnetSelection;
  readonly cluster: ecs.ICluster;
  readonly repositories: Record<'api' | 'web' | 'nginx', ecr.IRepository>;
  readonly proxyEndpoint: string;
  readonly loginSecrets: Record<string, secretsmanager.ISecret>;
  readonly workersSecurityGroup: ec2.ISecurityGroup;
  /** Optional existing SNS topic for alarms (else one is created). */
  readonly alarmTopic?: sns.ITopic;
}

/**
 * MaintenanceStack (PLAN item 8).
 *
 * run-task task definitions for migrate, provision-roles, staff, rekey, monitor
 * (five one-off maintenance tasks), plus a reconcile task definition driven by
 * an EventBridge Scheduler cron (~00:30 America/New_York) as a Fargate
 * run-task, with a CloudWatch alarm on a non-zero reconcile exit wired to SNS.
 *
 * These are task definitions only — CDK never runs them. The operator invokes
 * them via infra/scripts/run-task.sh (migrate → provision-roles order).
 */
export class MaintenanceStack extends cdk.Stack {
  public readonly taskDefinitions: Record<string, ecs.FargateTaskDefinition>;
  public readonly reconcileSchedule: scheduler.CfnSchedule;
  public readonly reconcileExitAlarm: cloudwatch.Alarm;
  public readonly alarmTopic: sns.ITopic;

  constructor(scope: Construct, id: string, props: MaintenanceStackProps) {
    super(scope, id, props);

    const {
      config,
      vpc,
      computeSubnets,
      cluster,
      repositories,
      proxyEndpoint,
      loginSecrets,
      workersSecurityGroup,
    } = props;

    const factory = new TaskFactory(this, repositories);
    this.taskDefinitions = {};

    const log = new logs.LogGroup(this, 'MaintenanceLogs', {
      logGroupName: `/dad/${config.env}/maintenance`,
      retention:
        config.env === 'production'
          ? logs.RetentionDays.ONE_YEAR
          : logs.RetentionDays.ONE_MONTH,
      removalPolicy: config.deletionProtection
        ? cdk.RemovalPolicy.RETAIN
        : cdk.RemovalPolicy.DESTROY,
    });

    const dbUrl = (login: ProcessLogin): string => {
      const secret = loginSecrets[login.login];
      const password = secret.secretValueFromJson('password').unsafeUnwrap();
      return buildDatabaseUrl({
        login: login.login,
        password,
        proxyEndpoint,
        db: config.dbName,
      });
    };

    const ownerUrl = dbUrl(OWNER_LOGIN);

    // Env for provision-roles: owner + every per-process DONOR_*_DATABASE_URL.
    const provisionEnv: Record<string, string> = {
      [OWNER_LOGIN.envVar]: ownerUrl,
    };
    for (const p of Object.values(PROCESS_LOGINS)) {
      provisionEnv[p.envVar] = dbUrl(p);
    }

    const defineTask = (
      taskId: string,
      command: string[],
      environment: Record<string, string>,
    ): ecs.FargateTaskDefinition => {
      const { taskDefinition } = factory.build({
        id: `maint-${taskId}`,
        image: 'api',
        size: config.taskSizes.worker,
        command,
        environment,
        logGroup: log,
        logStreamPrefix: taskId,
      });
      this.taskDefinitions[taskId] = taskDefinition;
      return taskDefinition;
    };

    // --- five one-off maintenance task definitions ----------------------
    defineTask('migrate', ['migrate'], {
      [OWNER_LOGIN.envVar]: ownerUrl,
    });
    defineTask('provision-roles', ['provision'], provisionEnv);
    defineTask('staff', ['staff'], {
      DONOR_STAFF_ORIGIN: config.staffOrigin,
      [PROCESS_LOGINS.staff.envVar]: dbUrl(PROCESS_LOGINS.staff),
    });
    defineTask('rekey', ['rekey'], {
      // Encryption keys are crown jewels; wired as plain env placeholders here
      // and supplied from Secrets Manager at run time via run-task overrides.
      DONOR_EMAIL_ENCRYPTION_KEY: '',
      DONOR_CONTENT_ENCRYPTION_KEY: '',
      [PROCESS_LOGINS.rekey.envVar]: dbUrl(PROCESS_LOGINS.rekey),
    });
    defineTask('monitor', ['monitor'], {
      [PROCESS_LOGINS.monitor.envVar]: dbUrl(PROCESS_LOGINS.monitor),
    });

    // --- reconcile task definition --------------------------------------
    const reconcileTask = defineTask('reconcile', ['reconcile'], {
      STRIPE_SECRET_KEY: '',
      DONOR_STRIPE_REQUESTS_PER_SECOND: '25',
      [PROCESS_LOGINS.reconcile.envVar]: dbUrl(PROCESS_LOGINS.reconcile),
    });

    // --- SNS topic for alarms -------------------------------------------
    this.alarmTopic =
      props.alarmTopic ??
      new sns.Topic(this, 'MaintenanceAlarms', {
        topicName: `dad-${config.env}-maintenance-alarms`,
        displayName: `DAD ${config.env} maintenance alarms`,
      });

    // --- EventBridge Scheduler: reconcile daily ~00:30 America/New_York --
    const schedulerRole = new iam.Role(this, 'ReconcileSchedulerRole', {
      assumedBy: new iam.ServicePrincipal('scheduler.amazonaws.com'),
    });
    reconcileTask.grantRun(schedulerRole);

    const subnets = vpc.selectSubnets(computeSubnets);

    this.reconcileSchedule = new scheduler.CfnSchedule(
      this,
      'ReconcileSchedule',
      {
        flexibleTimeWindow: { mode: 'OFF' },
        scheduleExpression: 'cron(30 0 * * ? *)',
        scheduleExpressionTimezone: 'America/New_York',
        description:
          'DAD reconcile: daily Stripe↔DB reconciliation (non-zero exit on discrepancy).',
        target: {
          arn: cluster.clusterArn,
          roleArn: schedulerRole.roleArn,
          ecsParameters: {
            taskDefinitionArn: reconcileTask.taskDefinitionArn,
            launchType: 'FARGATE',
            taskCount: 1,
            networkConfiguration: {
              awsvpcConfiguration: {
                assignPublicIp: 'DISABLED',
                subnets: subnets.subnetIds,
                securityGroups: [workersSecurityGroup.securityGroupId],
              },
            },
          },
        },
      },
    );

    // --- CloudWatch alarm on non-zero reconcile exit --------------------
    // A failed Fargate task surfaces as a stopped task; we alarm on the
    // ECS "task stopped with non-zero exit" signal via a metric filter on the
    // reconcile log group plus the ECS container-exit metric. Here we use a
    // custom metric-filter-driven alarm on the reconcile log group for the
    // discrepancy marker the reconcile job logs on non-zero exit.
    const exitMetricFilter = log.addMetricFilter('ReconcileNonZeroExit', {
      filterName: `dad-${config.env}-reconcile-nonzero-exit`,
      metricNamespace: `DAD/${config.env}/Reconcile`,
      metricName: 'NonZeroExit',
      filterPattern: logs.FilterPattern.anyTerm(
        'reconcile discrepancy',
        'exit status 1',
        'FATAL',
      ),
      metricValue: '1',
      defaultValue: 0,
    });

    this.reconcileExitAlarm = new cloudwatch.Alarm(this, 'ReconcileExitAlarm', {
      alarmName: `dad-${config.env}-reconcile-nonzero-exit`,
      alarmDescription:
        'Reconcile task reported a discrepancy (non-zero exit). Investigate Stripe↔DB drift.',
      metric: exitMetricFilter.metric({
        statistic: 'Sum',
        period: cdk.Duration.hours(1),
      }),
      threshold: 1,
      evaluationPeriods: 1,
      comparisonOperator:
        cloudwatch.ComparisonOperator.GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });
    this.reconcileExitAlarm.addAlarmAction(
      new cwActions.SnsAction(this.alarmTopic),
    );
  }
}
