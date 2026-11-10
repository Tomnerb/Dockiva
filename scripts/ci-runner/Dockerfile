# Self-hosted GitHub Actions runner for Dockiva (linux/arm64).
# One container = one job: run-dockiva-runner.sh starts it with a single-use
# JIT config and removes it when the job ends.
FROM ubuntu:24.04

ARG RUNNER_VERSION=2.338.2
ARG RUNNER_SHA256=670adb2f268fe21dcbb58b3db0e0aa9d5b3bddfe2c651481336e561ff86de945

# ponytail: Dockiva <= 0.6.7 keeps container files on case-insensitive APFS, so
# linux-libc-dev's xt_CONNMARK.h / xt_connmark.h pairs collide and dpkg fails.
# Skip the uppercase netfilter target headers; drop this once Dockiva ships
# the case-sensitive engine store (engine af61a4e).
RUN printf '%s\n' \
      'path-exclude=/usr/include/linux/netfilter/xt_[A-Z]*' \
      'path-exclude=/usr/include/linux/netfilter_ipv4/ipt_[A-Z]*' \
      'path-exclude=/usr/include/linux/netfilter_ipv6/ip6t_[A-Z]*' \
      > /etc/dpkg/dpkg.cfg.d/dockiva-case-insensitive

RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      build-essential ca-certificates curl git jq pkg-config sudo tar unzip \
      xz-utils zstd \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /home/runner
RUN curl -fsSL -o runner.tgz \
      "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-arm64-${RUNNER_VERSION}.tar.gz" \
 && echo "${RUNNER_SHA256}  runner.tgz" | sha256sum -c - \
 && tar xzf runner.tgz && rm runner.tgz \
 && ./bin/installdependencies.sh \
 && rm -rf /var/lib/apt/lists/*

# Root, not a sudo user: on Dockiva Engine, once apt drops to _apt (which sets
# no_new_privs), later sudo calls in the container can't regain root. Each job
# gets a fresh container, so root here doesn't outlive the job.
ENV RUNNER_ALLOW_RUNASROOT=1
ENTRYPOINT ["./run.sh"]
