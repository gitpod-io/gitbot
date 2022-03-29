#!/usr/bin/env bash

set -ex

TAG="${TAG:-dev}"

cp -rf ../common common
docker build -t eu.gcr.io/gitpod-core-dev/prow/deployed-labeler:$TAG .
rm -rf common
