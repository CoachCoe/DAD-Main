import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ecs from 'aws-cdk-lib/aws-ecs';
import * as ecr from 'aws-cdk-lib/aws-ecr';
import * as logs from 'aws-cdk-lib/aws-logs';
import { TaskSize } from './config';

/** Which built image a task runs. */
export type ImageName = 'api' | 'web' | 'nginx';

/** Inputs for building one Fargate task definition + its container. */
export interface TaskDefinitionSpec {
  /** Logical id for the task definition construct. */
  readonly id: string;
  /** The image repository to pull from (dad/api, dad/web, dad/nginx). */
  readonly image: ImageName;
  /** Optional image tag; defaults to 'latest' for offline synth. */
  readonly imageTag?: string;
  /** Fargate CPU/memory sizing. */
  readonly size: TaskSize;
  /**
   * Container command override. Omit to use the image's default CMD
   * (api → `api`, web → `node server.js`, nginx → entrypoint).
   */
  readonly command?: string[];
  /** Plain (non-secret) environment variables. */
  readonly environment?: Record<string, string>;
  /** Secret environment variables (valueFrom Secrets Manager). */
  readonly secrets?: Record<string, ecs.Secret>;
  /** Container ports to expose (e.g. 8002 for api, 8080/8443 for nginx). */
  readonly portMappings?: ecs.PortMapping[];
  /** The log group this task writes to. */
  readonly logGroup: logs.ILogGroup;
  /** Log stream prefix (defaults to the task id). */
  readonly logStreamPrefix?: string;
}

/**
 * A reusable Fargate task-definition factory (PLAN item 7).
 *
 * Centralizes the repeated wiring every DAD task needs: Fargate CPU/memory,
 * a single container from one of the three ECR repos running as the Dockerfile
 * uid, awslogs to the per-service log group, env + task secrets, and port
 * mappings. Both the services stack and the maintenance stack build their task
 * definitions through this so the shape stays identical.
 */
export class TaskFactory {
  constructor(
    private readonly scope: Construct,
    private readonly repositories: Record<ImageName, ecr.IRepository>,
  ) {}

  /** Build a Fargate task definition with a single primary container. */
  public build(spec: TaskDefinitionSpec): {
    taskDefinition: ecs.FargateTaskDefinition;
    container: ecs.ContainerDefinition;
  } {
    const taskDefinition = new ecs.FargateTaskDefinition(
      this.scope,
      `${spec.id}-TaskDef`,
      {
        cpu: spec.size.cpu,
        memoryLimitMiB: spec.size.memoryMiB,
        family: `dad-${spec.id}`,
      },
    );

    const container = taskDefinition.addContainer(`${spec.id}-Container`, {
      image: ecs.ContainerImage.fromEcrRepository(
        this.repositories[spec.image],
        spec.imageTag ?? 'latest',
      ),
      command: spec.command,
      environment: spec.environment,
      secrets: spec.secrets,
      logging: ecs.LogDrivers.awsLogs({
        logGroup: spec.logGroup,
        streamPrefix: spec.logStreamPrefix ?? spec.id,
      }),
    });

    for (const pm of spec.portMappings ?? []) {
      container.addPortMappings(pm);
    }

    // Grant the task permission to pull its image from ECR.
    this.repositories[spec.image].grantPull(taskDefinition.taskRole);

    return { taskDefinition, container };
  }
}

/**
 * Convert a Secrets Manager secret holding a JSON blob with a `password` field
 * into an ECS task secret. Shared helper for DB password injection, though DB
 * URLs themselves are injected as plain env (the URL embeds the password, built
 * from the resolved secret value via CfnDynamicReference where needed).
 */
export function jsonFieldSecret(
  secret: { secretValueFromJson: (key: string) => cdk.SecretValue },
  field: string,
): cdk.SecretValue {
  return secret.secretValueFromJson(field);
}
