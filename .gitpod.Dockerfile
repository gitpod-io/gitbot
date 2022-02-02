FROM gitpod/workspace-full:commit-f2d623ca9d270c2ce8560d2ca0f9ce71b105aff2

USER root

RUN apt-get update && \
    apt-get install -y curl gnupg2 software-properties-common unzip zip sudo make jq

### Docker client ###
RUN curl -fsSL https://download.docker.com/linux/ubuntu/gpg | apt-key add - \
    # 'cosmic' not supported
    && add-apt-repository -yu "deb [arch=amd64] https://download.docker.com/linux/ubuntu focal stable" \
    && apt-get install -yq docker-ce-cli \
    && apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/*

### Helm ###
RUN curl -fsSL https://get.helm.sh/helm-v3.7.0-linux-amd64.tar.gz \
    | tar -xzvC /usr/local/bin --strip-components=1 \
    && helm completion bash > /usr/share/bash-completion/completions/helm

### kubernetes ###
# https://github.com/kubernetes/kubernetes/releases/
RUN mkdir -p /usr/local/kubernetes/ && \
    curl -fsSL https://github.com/kubernetes/kubernetes/releases/download/v1.22.4/kubernetes.tar.gz | \
    tar -xzvC /usr/local/kubernetes/ --strip-components=1 && \
    KUBERNETES_SKIP_CONFIRM=true /usr/local/kubernetes/cluster/get-kube-binaries.sh
ENV PATH=$PATH:/usr/local/kubernetes/cluster/:/usr/local/kubernetes/client/bin/

## terraform
# https://releases.hashicorp.com/terraform/
RUN curl -fsSL https://apt.releases.hashicorp.com/gpg | apt-key add - && \
    apt-add-repository "deb [arch=$(dpkg --print-architecture)] https://apt.releases.hashicorp.com $(lsb_release -cs) main" && \
    apt-get update && apt-get install -y terraform

RUN curl -o /usr/bin/kubectx https://raw.githubusercontent.com/ahmetb/kubectx/master/kubectx && chmod +x /usr/bin/kubectx \
 && curl -o /usr/bin/kubens  https://raw.githubusercontent.com/ahmetb/kubectx/master/kubens  && chmod +x /usr/bin/kubens

# yq - jq for YAML files
RUN cd /usr/bin && curl -L https://github.com/mikefarah/yq/releases/download/2.4.0/yq_linux_amd64 > yq && chmod +x yq

# Bazel
RUN apt-get install -y apt-transport-https curl gnupg && \
    curl -fsSL https://bazel.build/bazel-release.pub.gpg | gpg --dearmor > bazel.gpg && \
    mv bazel.gpg /etc/apt/trusted.gpg.d/ && \
    echo "deb [arch=amd64] https://storage.googleapis.com/bazel-apt stable jdk1.8" | tee /etc/apt/sources.list.d/bazel.list && \
    apt-get update && \
    apt-get install -y bazel

# Go
RUN curl -L https://golang.org/dl/go1.17.2.linux-amd64.tar.gz | tar -C /usr/local -xzv
ENV PATH=$PATH:/usr/local/go/bin

USER gitpod

# Go
ENV GOFLAGS="-mod=readonly"

### Google Cloud ###
# https://cloud.google.com/sdk/docs/downloads-versioned-archives
ARG GCS_DIR=/opt/google-cloud-sdk
ENV PATH=$GCS_DIR/bin:$PATH
RUN sudo chown gitpod: /opt \
    && mkdir $GCS_DIR \
    && curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-354.0.0-linux-x86_64.tar.gz \
    | tar -xzvC /opt \
    && /opt/google-cloud-sdk/install.sh --quiet --usage-reporting=false --bash-completion=true \
    --additional-components docker-credential-gcr alpha beta \
    # needed for access to our private registries
    && docker-credential-gcr configure-docker
