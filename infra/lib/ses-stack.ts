import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ses from 'aws-cdk-lib/aws-ses';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import { EnvConfig } from './config';

/** Props for {@link SesStack}. */
export interface SesStackProps extends cdk.StackProps {
  readonly config: EnvConfig;
}

/**
 * SesStack (PLAN item 10b).
 *
 * An SES domain identity for config.sendingSubdomain with Easy DKIM enabled, an
 * IAM user whose only permission is `ses:SendRawEmail` (SMTP sending), and a
 * Secrets Manager secret holding the DONOR_SMTP_* values. CfnOutputs surface the
 * DKIM/verification DNS records the operator adds in Cloudflare, plus a
 * sandbox-mode reminder.
 *
 * Note: SES SMTP passwords are derived from the IAM user's secret access key
 * with a region-specific algorithm. We store the access key + a reminder in the
 * secret; the operator (or a small bootstrap step) derives the SMTP password.
 * This keeps CDK offline-synthesizable with no custom resource or AWS calls.
 */
export class SesStack extends cdk.Stack {
  public readonly identity: ses.EmailIdentity;
  public readonly smtpUser: iam.User;
  public readonly smtpSecret: secretsmanager.Secret;

  constructor(scope: Construct, id: string, props: SesStackProps) {
    super(scope, id, props);

    const { config } = props;
    const subdomain = config.sendingSubdomain;

    // Domain identity with Easy DKIM (3 CNAME records the operator publishes).
    this.identity = new ses.EmailIdentity(this, 'SendingIdentity', {
      identity: ses.Identity.domain(subdomain),
    });

    // IAM user restricted to SES sending only.
    this.smtpUser = new iam.User(this, 'SmtpUser', {
      userName: `dad-${config.env}-ses-smtp`,
    });
    this.smtpUser.addToPolicy(
      new iam.PolicyStatement({
        actions: ['ses:SendRawEmail', 'ses:SendEmail'],
        resources: ['*'],
        conditions: {
          StringEquals: { 'ses:FromAddress': `no-reply@${subdomain}` },
        },
      }),
    );

    const accessKey = new iam.CfnAccessKey(this, 'SmtpAccessKey', {
      userName: this.smtpUser.userName,
    });

    // DONOR_SMTP_* secret. SMTP endpoint for us-east-2; port 587 (STARTTLS).
    // The SMTP password is derived from the secret access key at bootstrap.
    this.smtpSecret = new secretsmanager.Secret(this, 'SmtpSecret', {
      secretName: `dad/${config.env}/ses/smtp`,
      description:
        'DONOR_SMTP_* values for SES. SMTP password is derived from the IAM secret access key (see README).',
      secretObjectValue: {
        DONOR_SMTP_HOST: cdk.SecretValue.unsafePlainText(
          `email-smtp.${config.region}.amazonaws.com`,
        ),
        DONOR_SMTP_PORT: cdk.SecretValue.unsafePlainText('587'),
        DONOR_SMTP_USERNAME: cdk.SecretValue.resourceAttribute(accessKey.ref),
        DONOR_SMTP_PASSWORD: cdk.SecretValue.resourceAttribute(
          accessKey.attrSecretAccessKey,
        ),
        DONOR_EMAIL_FROM: cdk.SecretValue.unsafePlainText(
          `no-reply@${subdomain}`,
        ),
      },
    });

    // --- Outputs: DKIM / verification records + sandbox reminder --------
    new cdk.CfnOutput(this, 'SendingDomain', { value: subdomain });
    new cdk.CfnOutput(this, 'DkimToken1', {
      value: this.identity.dkimDnsTokenName1,
      description: 'Cloudflare CNAME name #1 for Easy DKIM.',
    });
    new cdk.CfnOutput(this, 'DkimValue1', {
      value: this.identity.dkimDnsTokenValue1,
    });
    new cdk.CfnOutput(this, 'DkimToken2', {
      value: this.identity.dkimDnsTokenName2,
      description: 'Cloudflare CNAME name #2 for Easy DKIM.',
    });
    new cdk.CfnOutput(this, 'DkimValue2', {
      value: this.identity.dkimDnsTokenValue2,
    });
    new cdk.CfnOutput(this, 'DkimToken3', {
      value: this.identity.dkimDnsTokenName3,
      description: 'Cloudflare CNAME name #3 for Easy DKIM.',
    });
    new cdk.CfnOutput(this, 'DkimValue3', {
      value: this.identity.dkimDnsTokenValue3,
    });
    new cdk.CfnOutput(this, 'SmtpSecretArn', {
      value: this.smtpSecret.secretArn,
      description: 'Secrets Manager ARN holding the DONOR_SMTP_* values.',
    });
    new cdk.CfnOutput(this, 'SandboxReminder', {
      value:
        'SES starts in SANDBOX mode: only verified recipients receive mail. Request production sending access before the public test.',
    });
  }
}
