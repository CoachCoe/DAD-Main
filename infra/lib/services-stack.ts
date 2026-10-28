import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as ecs from 'aws-cdk-lib/aws-ecs';
import * as ecr from 'aws-cdk-lib/aws-ecr';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import { EnvConfig } from './config';
import { buildDatabaseUrl, PROCESS_LOGINS, ProcessLogin } from './db-url';
import { ServiceName } from './ecs-cluster-stack';
import { TaskFactory } from './task-factory';

/** Props for {@link ServicesStack}. */
export interface ServicesStackProps extends cdk.StackProps {
  readonly config: EnvConfig;
  readonly vpc: ec2.IVpc;
  readonly computeSubnets: ec2.SubnetSelection;
  readonly cluster: ecs.ICluster;
  readonly namespaceName: string;
  readonly repositories: Record<'api' | 'web' | 'nginx', ecr.IRepository>;
  readonly logGroups: Record<ServiceName, logs.ILogGroup>;
  /** RDS Proxy endpoint host used to build per-process DB URLs. */
  readonly proxyEndpoint: string;
  /** DB login secrets keyed by login name (from the data stack). */
  readonly loginSecrets: Record<string, secretsmanager.ISecret>;
  /** CIDRs of the web/compute tier for DONOR_TRUSTED_PROXY_CIDRS. */
  readonly computeSubnetCidrs: string[];
  /** SGs from the network stack. */
  readonly nginxSecurityGroup: ec2.ISecurityGroup;
  readonly webSecurityGroup: ec2.ISecurityGroup;
  readonly apiSecurityGroup: ec2.ISecurityGroup;
  readonly workersSecurityGroup: ec2.ISecurityGroup;
}

/** Internal description of one worker service. */
interface WorkerSpec {
  readonly service: ServiceName;
  readonly kind: string;
  readonly processId: keyof typeof PROCESS_LOGINS & string;
  /** Whether this worker needs the mail-transport env (SMTP + public origin). */
  readonly mail: boolean;
}

/**
 * ServicesStack (PLAN item 7).
 *
 * Exactly nine long-running Fargate services matching docker-compose.prod.yml:
 * api, web, nginx, and the six workers. Each is built through the shared
 * {@link TaskFactory}. The api is registered in Service Connect as `api` so web
 * resolves `http://api:8002`; the SG chain (from the network stack) already
 * restricts api:8002 to the web tier only.
 */
export class ServicesStack extends cdk.Stack {
  public readonly services: Record<ServiceName, ecs.FargateService>;

  constructor(scope: Construct, id: string, props: ServicesStackProps) {
    super(scope, id, props);

    const {
      config,
      vpc,
      computeSubnets,
      cluster,
      namespaceName,
      repositories,
      logGroups,
      proxyEndpoint,
      loginSecrets,
      computeSubnetCidrs,
      nginxSecurityGroup,
      webSecurityGroup,
      apiSecurityGroup,
      workersSecurityGroup,
    } = props;

    const factory = new TaskFactory(this, repositories);
    this.services = {} as Record<ServiceName, ecs.FargateService>;

    // Build a per-process DB URL from the login secret's password field.
    // The URL is a plain env var (as in Compose); the password resolves at
    // deploy time via a {{resolve:secretsmanager:...}} dynamic reference.
    const dbUrl = (processId: string): string => {
      const login: ProcessLogin = PROCESS_LOGINS[processId];
      const secret = loginSecrets[login.login];
      const password = secret.secretValueFromJson('password').unsafeUnwrap();
      return buildDatabaseUrl({
        login: login.login,
        password,
        proxyEndpoint,
        db: config.dbName,
      });
    };

    const trustedProxyCidrs = computeSubnetCidrs.join(',');

    // --- api ------------------------------------------------------------
    const api = factory.build({
      id: 'api',
      image: 'api',
      size: config.taskSizes.api,
      // image default CMD is `api`.
      environment: {
        DONOR_GO_LISTEN: '0.0.0.0:8002',
        DONOR_PUBLIC_ORIGIN: config.publicOrigin,
        DONOR_STAFF_ORIGIN: config.staffOrigin,
        DONOR_TRUSTED_PROXY_CIDRS: trustedProxyCidrs,
        DONOR_API_DATABASE_URL: dbUrl('api'),
      },
      portMappings: [{ containerPort: 8002, name: 'api' }],
      logGroup: logGroups.api,
    });
    this.services.api = new ecs.FargateService(this, 'ApiService', {
      cluster,
      serviceName: 'api',
      taskDefinition: api.taskDefinition,
      desiredCount: config.desiredCounts.api,
      vpcSubnets: computeSubnets,
      securityGroups: [apiSecurityGroup],
      // Register `api` in Service Connect so web resolves http://api:8002.
      serviceConnectConfiguration: {
        namespace: namespaceName,
        services: [
          {
            portMappingName: 'api',
            dnsName: 'api',
            port: 8002,
          },
        ],
      },
    });

    // --- web ------------------------------------------------------------
    const web = factory.build({
      id: 'web',
      image: 'web',
      size: config.taskSizes.web,
      // image default CMD is `node server.js`.
      environment: {
        DONOR_API_BASE_URL: 'http://api:8002',
        DONOR_CLIENT_IP_HEADER: 'X-Real-IP',
      },
      portMappings: [{ containerPort: 3000, name: 'web' }],
      logGroup: logGroups.web,
    });
    this.services.web = new ecs.FargateService(this, 'WebService', {
      cluster,
      serviceName: 'web',
      taskDefinition: web.taskDefinition,
      desiredCount: config.desiredCounts.web,
      vpcSubnets: computeSubnets,
      securityGroups: [webSecurityGroup],
      // web is a Service Connect client (resolves api) but publishes nothing.
      serviceConnectConfiguration: { namespace: namespaceName },
    });

    // --- nginx ----------------------------------------------------------
    const nginx = factory.build({
      id: 'nginx',
      image: 'nginx',
      size: config.taskSizes.nginx,
      environment: {
        INGRESS_MODE: 'cloudflare',
        DONOR_PUBLIC_ORIGIN: config.publicOrigin,
        DONOR_STAFF_ORIGIN: config.staffOrigin,
        NGINX_ENVSUBST_FILTER: '^INGRESS_',
        NGINX_ENTRYPOINT_QUIET_LOGS: '1',
      },
      portMappings: [
        { containerPort: 8080, name: 'http' },
        { containerPort: 8443, name: 'https' },
        { containerPort: 8081, name: 'health' },
      ],
      logGroup: logGroups.nginx,
    });
    this.services.nginx = new ecs.FargateService(this, 'NginxService', {
      cluster,
      serviceName: 'nginx',
      taskDefinition: nginx.taskDefinition,
      desiredCount: config.desiredCounts.nginx,
      vpcSubnets: computeSubnets,
      securityGroups: [nginxSecurityGroup],
    });

    // --- the six workers ------------------------------------------------
    const workers: WorkerSpec[] = [
      {
        service: 'stripe-worker',
        kind: 'stripe',
        processId: 'worker-stripe',
        mail: false,
      },
      {
        service: 'projection-worker',
        kind: 'projections',
        processId: 'worker-projections',
        mail: false,
      },
      {
        service: 'login-email-worker',
        kind: 'login-email',
        processId: 'worker-login-email',
        mail: true,
      },
      {
        service: 'email-change-worker',
        kind: 'email-changes',
        processId: 'worker-email-changes',
        mail: true,
      },
      {
        service: 'message-worker',
        kind: 'messages',
        processId: 'worker-messages',
        mail: true,
      },
      {
        service: 'push-worker',
        kind: 'push',
        processId: 'worker-push',
        mail: false,
      },
    ];

    for (const w of workers) {
      const login = PROCESS_LOGINS[w.processId];
      const environment: Record<string, string> = {
        DONOR_GO_WORKERS_ENABLED: 'true',
        [login.envVar]: dbUrl(w.processId),
      };
      if (w.service === 'stripe-worker') {
        environment.DONOR_STRIPE_REQUESTS_PER_SECOND = '25';
      }
      if (w.mail) {
        environment.DONOR_PUBLIC_ORIGIN = config.publicOrigin;
      }

      const built = factory.build({
        id: w.service,
        image: 'api',
        size: config.taskSizes.worker,
        command: ['worker', '--kind', w.kind],
        environment,
        logGroup: logGroups[w.service],
      });

      this.services[w.service] = new ecs.FargateService(
        this,
        `${w.service}-Service`,
        {
          cluster,
          serviceName: w.service,
          taskDefinition: built.taskDefinition,
          desiredCount: config.desiredCounts.workers,
          vpcSubnets: computeSubnets,
          securityGroups: [workersSecurityGroup],
        },
      );
    }

    // Keep the VPC reference meaningful for reviewers / future wiring.
    void vpc;
  }
}
