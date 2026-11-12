# syntax=docker/dockerfile:1.7
# Federation runs goatbuilder federate from the core host, so the image carries
# what an operator's machine would: goatbuilder (the released binary), Ansible
# with its collections, the AWS SSM plugin, git, ssh and the docker client.
#
#   docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t federation:dev .

FROM golang:1.26-trixie AS app
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/federation ./cmd/federation

FROM docker:29-cli AS dockercli

FROM debian:trixie-slim
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git openssh-client openssl jq iputils-ping \
      python3 python3-venv python3-pip tini \
    && rm -rf /var/lib/apt/lists/*
# Ansible and the Python the peering plays and scripts use, in one venv.
RUN python3 -m venv /opt/ansible \
    && /opt/ansible/bin/pip install --no-cache-dir \
         "ansible-core==2.20.*" boto3 botocore cryptography pyyaml jmespath netaddr \
    && /opt/ansible/bin/ansible-galaxy collection install -p /opt/ansible/collections \
         amazon.aws community.aws ansible.posix community.crypto community.docker community.general
# The SSM plugin for the forwarder (ansible_connection=amazon.aws.aws_ssm).
RUN arch=$([ "$TARGETARCH" = arm64 ] && echo arm64 || echo 64bit) \
    && curl -fsSL -o /tmp/ssm.deb "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/ubuntu_${arch}/session-manager-plugin.deb" \
    && dpkg -i /tmp/ssm.deb && rm /tmp/ssm.deb
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
# Preflight requires compose (the edge role deploys with `docker compose`).
COPY --from=dockercli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/libexec/docker/cli-plugins/docker-compose
# goatbuilder federate does the federation. The digests are from the release's
# SHA256SUMS, whose keyless cosign signature by goatcore's release workflow was
# verified when they were pinned here.
ARG GOATBUILDER_VERSION=0.5.1
ARG GOATBUILDER_SHA256_ARM64=9c53d576dc0ce5eb5eb30a01fc28d3f607c35c975553d3187e9f3bd8d2b36d48
ARG GOATBUILDER_SHA256_AMD64=cce77bed0e0237cb3d7e07f8ce3fc66297c2aa0d32cee0e8168bcbeb23ccdbd2
RUN case "$TARGETARCH" in arm64) sum=$GOATBUILDER_SHA256_ARM64 ;; amd64) sum=$GOATBUILDER_SHA256_AMD64 ;; *) echo "no goatbuilder digest pinned for $TARGETARCH" >&2; exit 1 ;; esac \
    && curl -fsSL -o /usr/local/bin/goatbuilder \
         "https://github.com/USAREUR-AF/goatcore/releases/download/goatbuilder/v${GOATBUILDER_VERSION}/goatbuilder-linux-${TARGETARCH}" \
    && echo "$sum  /usr/local/bin/goatbuilder" | sha256sum -c - \
    && chmod 0755 /usr/local/bin/goatbuilder \
    && goatbuilder --version
COPY --from=app /out/federation /app/federation

ENV PATH=/opt/ansible/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    ANSIBLE_COLLECTIONS_PATH=/opt/ansible/collections \
    FED_SECRETS=/run/secrets/federation \
    PYTHONDONTWRITEBYTECODE=1
RUN groupadd --system --gid 65532 federation \
    && useradd --system --uid 65532 --gid 65532 --home-dir /var/lib/federation/home --shell /bin/bash federation
USER 65532
EXPOSE 8090
ENTRYPOINT ["/usr/bin/tini", "--", "/app/federation", "-config", "/etc/federation/config.json", "-listen", ":8090", "-state", "/var/lib/federation"]
