# deployed-labeler

> A go web server that labels Pull Requests with the `deployed: <team>` label

Accepts `POST` requests, while requiring to parameters:

- `commit`: Commit that has just been deployed to production.
- `team`: Which team just deployed to production.

`deployed-labeler` will look for the last 100 commits of the repository's default branch, alongside their associated Pull Requests and labels.

![image](https://user-images.githubusercontent.com/24193764/139254510-9f8ed8e1-e9ac-4177-b447-49932b804edd.png)

After that, it will add the `deployed: <team>` label only to the PRs where the `team` parameter matches with the already existing `team: <team>` label.

![image](https://user-images.githubusercontent.com/24193764/139254958-b8c08aee-3a51-477f-ac3c-8aad13bcd495.png)

## Developing

During development you can build the image locally and run it in dry-run mode. First build the image:

```sh
cd plugins/deployed-labeler
./dev/build-image.sh
```

To run it localy we'll need to have access to our GithHub token and HMAC for the webhook - we'll grab the one from the cluster for now:

```sh
BROWSER= gcloud auth login
gcloud container clusters get-credentials prow --zone europe-west1-b --project gitpod-core-dev
mkdir -p /tmp/secrets
kubectl -n prow get secret github-token -o jsonpath='{.data.token}' | base64 -d > /tmp/secrets/github-token
kubectl -n prow get secret hmac-token -o jsonpath='{.data.hmac:}' | base64 -d > /tmp/secrets/webhook-hmac-token
```

Now you can run it locally in `dry-run` mode.

```sh
docker run \
    -p 8080:8080 \
    -p 8081:8081 \
    --volume /tmp/secrets:/etc/deploy-labeler-secrets \
    eu.gcr.io/gitpod-core-dev/prow/deployed-labeler:dev \
        --dry-run=true \
        -hmac=/etc/deploy-labeler-secrets/webhook-hmac-token \
        --github-token-path=/etc/deploy-labeler-secrets/github-token \
        --github-endpoint=http://ghproxy \
        --github-endpoint=https://api.github.com
```

And `curl` the endpoint

```sh
curl -XPOST "http://localhost:8080/deployed?commit=01f4897c5323433e7831ca948f7d340c3c762885&team=webapp"
```

## Deploying

Building and deployment a new image is currently manual.

Authenticate to GCP

```
BROWSER= gcloud auth login
```

Now find an appropriate tag to use. You can list the current tags using the following.

```sh
gcloud container images list-tags eu.gcr.io/gitpod-core-dev/prow/deployed-labeler
```

Use the following command to build the image. In this example TAG is set to 1 but please increment the number accoringly based on the output of the command above.

```sh
TAG=1 ./dev/build-image.sh
```

Now push the image

```sh
TAG=1 ./dev/push-image.sh
```

Connect to the prow cluster in gitpod-core-dev

```sh
gcloud container clusters get-credentials prow --zone europe-west1-b --project gitpod-core-dev
```

Finally edit the deployment to update the image

```sh
kubectl -n prow edit deployment deployed-labeler
```
