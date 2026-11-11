# syntax=docker/dockerfile:1.7
# Federation runs the peering driver from the core host, so the image carries
# what the operator laptop had: goat and goatbuilder (built from the patched
# goatcore in the build context), Ansible with its collections, the AWS SSM
# plugin, git, ssh and the docker client.
#
# Build on the core (arm64), from a context holding this repo plus
# goatcore-src.tar.gz: `git archive` of goatcore 4b89370, which is PR #279
# (`init --over-ssh`) plus our SPIRE trust-domain fix (#247):
#   docker build -t federation:<rev> .

FROM rust:1.99-slim-trixie AS goat
RUN apt-get update && apt-get install -y --no-install-recommends pkg-config libssl-dev ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src/goatcore
COPY goatcore-src.tar.gz /tmp/
RUN tar xzf /tmp/goatcore-src.tar.gz -C /src/goatcore && rm /tmp/goatcore-src.tar.gz
WORKDIR /src/goatcore/crates
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/src/goatcore/crates/target \
    cargo build --release --locked -p goat-cli -p goatbuilder \
    && install -m0755 target/release/goat target/release/goatbuilder /usr/local/bin/

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
COPY --from=goat /usr/local/bin/goat /usr/local/bin/goatbuilder /usr/local/bin/
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
