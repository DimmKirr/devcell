# infra — Infrastructure-as-Code tools
# Runtimes managed by mise.
{pkgs, config, lib, ...}: let
  bin = config.devcell.managedMcp.nixBinPrefix;

  # AWS MCP servers via uvx wrappers.
  # uvx caches virtualenvs in ~/.cache/uv/ (persistent home mount) — first run downloads, then cached.
  # https://github.com/awslabs/mcp
  awsApiMcpServer = pkgs.writeShellScriptBin "aws-api-mcp-server" ''
    export AWS_PROFILE="''${SESSION_NAME:-''${AWS_PROFILE:-default}}"
    exec ${pkgs.uv}/bin/uvx awslabs.aws-api-mcp-server "$@"
  '';
  cloudwatchMcpServer = pkgs.writeShellScriptBin "cloudwatch-mcp-server" ''
    export AWS_PROFILE="''${SESSION_NAME:-''${AWS_PROFILE:-default}}"
    exec ${pkgs.uv}/bin/uvx awslabs.cloudwatch-mcp-server "$@"
  '';

  # AWS read-only session policy — used by credential_process to scope down creds.
  # Based on AWS managed ReadOnlyAccess: allows all read/list/describe/get actions.
  awsReadOnlyPolicy = pkgs.writeText "aws-readonly-policy.json" (builtins.toJSON {
    Version = "2012-10-17";
    Statement = [{
      Effect = "Allow";
      Action = [
        "acm:Describe*" "acm:Get*" "acm:List*"
        "autoscaling:Describe*"
        "cloudformation:Describe*" "cloudformation:Get*" "cloudformation:List*"
        "cloudfront:Get*" "cloudfront:List*"
        "cloudtrail:Describe*" "cloudtrail:Get*" "cloudtrail:List*" "cloudtrail:LookupEvents"
        "cloudwatch:Describe*" "cloudwatch:Get*" "cloudwatch:List*"
        "config:Describe*" "config:Get*" "config:List*"
        "dynamodb:Describe*" "dynamodb:Get*" "dynamodb:List*" "dynamodb:Query" "dynamodb:Scan"
        "ec2:Describe*" "ec2:Get*"
        "ecr:Describe*" "ecr:Get*" "ecr:List*" "ecr:BatchGetImage"
        "ecs:Describe*" "ecs:List*"
        "eks:Describe*" "eks:List*"
        "elasticache:Describe*" "elasticache:List*"
        "elasticloadbalancing:Describe*"
        "es:Describe*" "es:List*" "es:Get*"
        "events:Describe*" "events:List*"
        "iam:Get*" "iam:List*" "iam:Generate*"
        "kinesis:Describe*" "kinesis:Get*" "kinesis:List*"
        "kms:Describe*" "kms:Get*" "kms:List*"
        "lambda:Get*" "lambda:List*"
        "logs:Describe*" "logs:Get*" "logs:FilterLogEvents" "logs:StartQuery" "logs:GetQueryResults"
        "organizations:Describe*" "organizations:List*"
        "rds:Describe*" "rds:List*"
        "redshift:Describe*" "redshift:Get*"
        "route53:Get*" "route53:List*"
        "s3:Get*" "s3:List*" "s3:HeadBucket" "s3:HeadObject"
        "secretsmanager:Describe*" "secretsmanager:GetResourcePolicy" "secretsmanager:List*"
        "ses:Get*" "ses:List*" "ses:Describe*"
        "sns:Get*" "sns:List*"
        "sqs:Get*" "sqs:List*"
        "ssm:Describe*" "ssm:Get*" "ssm:List*"
        "sts:GetCallerIdentity" "sts:GetSessionToken" "sts:GetAccessKeyInfo"
        "tag:Get*"
        "waf:Get*" "waf:List*"
        "wafv2:Get*" "wafv2:List*" "wafv2:Describe*"
      ];
      Resource = "*";
    }];
  });

  # credential_process script — re-assumes current role with read-only session policy.
  # Called by AWS SDKs when AWS_CONFIG_FILE points to the generated config.
  # STRICT: no fallback. If scoping fails, all AWS calls fail. This guarantees
  # that when read_only=true, unscoped credentials can never be used.
  awsReadOnlyCredProcess = pkgs.writeShellScriptBin "aws-readonly-cred-process" ''
    set -euo pipefail
    AWS="${pkgs.awscli2}/bin/aws"
    JQ="${pkgs.jq}/bin/jq"
    POLICY_FILE="${awsReadOnlyPolicy}"

    # Get current identity to find the role ARN
    CALLER=$($AWS sts get-caller-identity --output json 2>/dev/null) || {
      echo "aws-readonly-cred-process: FATAL — failed to get caller identity. No AWS credentials available." >&2
      exit 1
    }
    ARN=$(echo "$CALLER" | $JQ -r '.Arn')

    # Extract role ARN from assumed-role ARN (arn:aws:sts::ACCT:assumed-role/NAME/SESSION → arn:aws:iam::ACCT:role/NAME)
    if echo "$ARN" | grep -q ':assumed-role/'; then
      ACCT=$(echo "$CALLER" | $JQ -r '.Account')
      ROLE_NAME=$(echo "$ARN" | sed 's|.*:assumed-role/||; s|/.*||')
      ROLE_ARN="arn:aws:iam::$ACCT:role/$ROLE_NAME"
    elif echo "$ARN" | grep -q ':role/'; then
      ROLE_ARN="$ARN"
    else
      echo "aws-readonly-cred-process: FATAL — identity is not a role ($ARN), cannot scope down to read-only." >&2
      echo "Set [aws] read_only = false in .devcell.toml to use unscoped credentials." >&2
      exit 1
    fi

    # Re-assume with read-only session policy
    RESULT=$($AWS sts assume-role \
      --role-arn "$ROLE_ARN" \
      --role-session-name "devcell-readonly" \
      --duration-seconds 3600 \
      --policy "file://$POLICY_FILE" \
      --output json 2>/dev/null) || {
      echo "aws-readonly-cred-process: FATAL — assume-role failed for $ROLE_ARN." >&2
      echo "The role's trust policy may not allow self-assumption." >&2
      echo "Set [aws] read_only = false in .devcell.toml to use unscoped credentials." >&2
      exit 1
    }

    # Output in credential_process JSON format
    echo "$RESULT" | $JQ '{
      Version: 1,
      AccessKeyId: .Credentials.AccessKeyId,
      SecretAccessKey: .Credentials.SecretAccessKey,
      SessionToken: .Credentials.SessionToken,
      Expiration: .Credentials.Expiration
    }'
  '';

  # opentofu-mcp-server: OpenTofu Registry MCP — module/provider search, docs, version lookup.
  # https://github.com/opentofu/opentofu-mcp-server
  opentofuSrc = pkgs.fetchFromGitHub {
    owner = "opentofu";
    repo = "opentofu-mcp-server";
    rev = "v1.0.0";
    hash = "sha256-qgjAnoduzAjvxgbgG8QW53CMF3/bW0NQhDbVv3ebntw=";
  };
  opentofuMcp = pkgs.stdenvNoCC.mkDerivation {
    pname = "opentofu-mcp-server";
    version = "1.0.0";
    src = opentofuSrc;
    pnpmDeps = pkgs.pnpm_9.fetchDeps {
      pname = "opentofu-mcp-server";
      version = "1.0.0";
      src = opentofuSrc;
      hash = "sha256-XvP7yJXmfm7+3/4i2fhjooJQk+18aHiZzjfmt4l+HyM=";
      fetcherVersion = 2;
    };
    nativeBuildInputs = [pkgs.pnpm_9.configHook pkgs.nodejs_22 pkgs.makeWrapper];
    buildPhase = "pnpm build";
    installPhase = ''
      mkdir -p $out/bin $out/lib
      cp -r . $out/lib/opentofu-mcp-server
      makeWrapper ${pkgs.nodejs_22}/bin/node $out/bin/opentofu-mcp-server \
        --add-flags $out/lib/opentofu-mcp-server/dist/local.js
    '';
  };
  # AWS config with credential_process for read-only scoping.
  # Placed at /opt/devcell/.aws/config; activated via AWS_CONFIG_FILE env var.
  awsReadOnlyConfig = pkgs.writeText "aws-config" ''
    [default]
    credential_process = /opt/devcell/.local/state/nix/profiles/profile/bin/aws-readonly-cred-process
  '';

in {
  imports = [./mise.nix];

  devcell.mise.tools.terraform = "1.14.3";
  devcell.mise.tools.opentofu = "1.10.6";

  # Place AWS config at /opt/devcell/.aws/config (nix-managed, read-only).
  # AWS SDKs use this when AWS_CONFIG_FILE is set by the runner.
  home.file.".aws/config".source = awsReadOnlyConfig;

  home.packages = with pkgs; [
    awsReadOnlyCredProcess  # credential_process for read-only AWS scoping
    awscli2  # AWS CLI v2 (use: aws)
    packer
    terraform-docs
    terraform-plugin-docs  # generates/validates Terraform provider docs (use: tfplugindocs)
    kubernetes-helm  # Kubernetes package manager (use: helm)
    opentofuMcp  # OpenTofu Registry MCP server (use: opentofu-mcp-server)
    awsApiMcpServer  # AWS API MCP server via uvx (use: aws-api-mcp-server)
    cloudwatchMcpServer  # CloudWatch MCP server via uvx (use: cloudwatch-mcp-server)
  ];

  # AWS API MCP — wraps all 200+ AWS services. Uses standard AWS credential chain.
  # READ_OPERATIONS_ONLY is inherited from container env (set by runner when [aws] read_only=true).
  devcell.managedMcp.servers."aws-api" = {
    command = "${bin}/aws-api-mcp-server";
    args = [];
  };

  # CloudWatch MCP — metrics, alarms, logs, analysis. Uses standard AWS credential chain.
  devcell.managedMcp.servers."cloudwatch" = {
    command = "${bin}/cloudwatch-mcp-server";
    args = [];
  };

  devcell.managedMcp.servers.opentofu = {
    command = "${bin}/opentofu-mcp-server";
    args = [];
  };
  # Notion — remote HTTP MCP server.
  # Auth: OAuth 2.1 flow on first use (run /mcp in Claude session to authenticate).
  devcell.managedMcp.servers.notion = {
    type = "http";
    url = "https://mcp.notion.com/mcp";
  };
}
