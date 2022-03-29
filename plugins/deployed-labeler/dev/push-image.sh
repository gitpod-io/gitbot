#!/usr/bin/env bash

set -euxo pipefail

docker push eu.gcr.io/gitpod-core-dev/prow/deployed-labeler:$TAG
