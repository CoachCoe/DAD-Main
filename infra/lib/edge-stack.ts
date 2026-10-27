import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as ecs from 'aws-cdk-lib/aws-ecs';
import * as elbv2 from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import { EnvConfig } from './config';

/** Props for {@link EdgeStack}. */
export interface EdgeStackProps extends cdk.StackProps {
  readonly config: EnvConfig;
  readonly vpc: ec2.IVpc;
  readonly publicSubnets: ec2.SubnetSelection;
  /** The nginx Fargate service the NLB targets. */
  readonly nginxService: ecs.FargateService;
  /** The nginx SG whose ingress the Cloudflare toggle restricts. */
  readonly nginxSecurityGroup: ec2.ISecurityGroup;
  /** The NLB-path SG from the network stack. */
  readonly nlbSecurityGroup: ec2.ISecurityGroup;
}

/**
 * EdgeStack (PLAN item 9).
 *
 * Internet-facing Network Load Balancer with TCP listeners 443→nginx:8443 and
 * 80→nginx:8080, targeting the nginx service. When
 * config.cloudflareIngressEnabled is set, the nginx SG's edge ingress is
 * restricted to Cloudflare's published ranges instead of the open internet.
 *
 * The ranges are a documented input (CDK context `cloudflareRanges` or the
 * default below) — never a hardcoded stale list. Refresh them with
 * scripts/refresh-cloudflare-ips.sh, which fetches
 * https://www.cloudflare.com/ips-v4 and ips-v6; feed the same lists here.
 */
export class EdgeStack extends cdk.Stack {
  public readonly loadBalancer: elbv2.NetworkLoadBalancer;
  public readonly httpsListener: elbv2.NetworkListener;
  public readonly httpListener: elbv2.NetworkListener;

  constructor(scope: Construct, id: string, props: EdgeStackProps) {
    super(scope, id, props);

    const {
      config,
      vpc,
      publicSubnets,
      nginxService,
      nginxSecurityGroup,
      nlbSecurityGroup,
    } = props;

    this.loadBalancer = new elbv2.NetworkLoadBalancer(this, 'Nlb', {
      vpc,
      vpcSubnets: publicSubnets,
      internetFacing: true,
      loadBalancerName: `dad-${config.env}-nlb`,
      securityGroups: [nlbSecurityGroup as ec2.SecurityGroup],
    });

    // HTTPS: TCP 443 → nginx 8443.
    this.httpsListener = this.loadBalancer.addListener('Https', {
      port: 443,
      protocol: elbv2.Protocol.TCP,
    });
    this.httpsListener.addTargets('NginxHttps', {
      port: 8443,
      protocol: elbv2.Protocol.TCP,
      targets: [nginxService.loadBalancerTarget({
        containerName: 'nginx-Container',
        containerPort: 8443,
      })],
      healthCheck: {
        protocol: elbv2.Protocol.HTTP,
        port: '8081',
        path: '/health',
      },
    });

    // HTTP: TCP 80 → nginx 8080.
    this.httpListener = this.loadBalancer.addListener('Http', {
      port: 80,
      protocol: elbv2.Protocol.TCP,
    });
    this.httpListener.addTargets('NginxHttp', {
      port: 8080,
      protocol: elbv2.Protocol.TCP,
      targets: [nginxService.loadBalancerTarget({
        containerName: 'nginx-Container',
        containerPort: 8080,
      })],
      healthCheck: {
        protocol: elbv2.Protocol.HTTP,
        port: '8081',
        path: '/health',
      },
    });

    // --- Cloudflare ingress toggle --------------------------------------
    // Ranges come from CDK context `cloudflareRanges` (comma/space separated)
    // or the documented placeholder default. Refresh via
    // scripts/refresh-cloudflare-ips.sh — do NOT hardcode a stale production
    // list. When the toggle is OFF we open the edge ports to the internet.
    const edgePorts = [
      { port: 8080, label: 'http' },
      { port: 8443, label: 'https' },
    ];

    if (config.cloudflareIngressEnabled) {
      for (const cidr of this.cloudflareRanges()) {
        const isV6 = cidr.includes(':');
        for (const { port, label } of edgePorts) {
          nginxSecurityGroup.addIngressRule(
            isV6 ? ec2.Peer.ipv6(cidr) : ec2.Peer.ipv4(cidr),
            ec2.Port.tcp(port),
            `nginx ${label} from Cloudflare ${cidr}`,
          );
        }
      }
    } else {
      for (const { port, label } of edgePorts) {
        nginxSecurityGroup.addIngressRule(
          ec2.Peer.anyIpv4(),
          ec2.Port.tcp(port),
          `nginx ${label} from internet (cloudflare toggle off)`,
        );
      }
    }

    new cdk.CfnOutput(this, 'NlbDnsName', {
      value: this.loadBalancer.loadBalancerDnsName,
      description:
        'NLB DNS name — point the Cloudflare DNS records for the public/staff hosts at this.',
    });
  }

  /**
   * Resolve the Cloudflare ranges list. Prefer CDK context so operators can
   * pass the freshly fetched list; otherwise use a small documented default
   * (a representative subset, NOT the authoritative list). The authoritative
   * source is scripts/refresh-cloudflare-ips.sh → https://www.cloudflare.com/ips-v4
   * and ips-v6.
   */
  private cloudflareRanges(): string[] {
    const ctx = this.node.tryGetContext('cloudflareRanges') as
      | string
      | string[]
      | undefined;
    if (Array.isArray(ctx)) {
      return ctx;
    }
    if (typeof ctx === 'string' && ctx.trim() !== '') {
      return ctx.split(/[\s,]+/).filter((s) => s !== '');
    }
    // Documented placeholder default (subset of Cloudflare's published ranges).
    // Replace at deploy time with the output of refresh-cloudflare-ips.sh.
    return ['173.245.48.0/20', '103.21.244.0/22', '2400:cb00::/32'];
  }
}
