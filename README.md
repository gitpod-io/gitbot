# Gitbot keeps a clean shop

Gitbot is the prow installation used for developing Gitpod. It automates PR handling and the likes.
It's available at https://prow.gitpod-dev.com


## HowTo

### Update the config?
Raise a PR that makes changes to `config/config.yaml` or `config/plugins.yaml`. Once that PR is merged, prow will pick up the changes automatically.

### Update the kubernetes objects?
```bash
gcloud auth login
gcloud container clusters get-credentials prow --zone europe-west1-b --project gitpod-core-dev
sh apply.sh
```

### Update the custom plugins?
```bash
# set up creds for pushing the new image
gcloud auth login
gcloud auth configure-docker

# rebuild the plugin (e.g. groundwork)
cd plugins/groundwork
./build.sh

# restart the plugin deployment
gcloud container clusters get-credentials prow --zone europe-west1-b --project gitpod-core-dev
kubectl config set-context --current --namespace=prow
kubectl rollout restart deployment groundwork
```

### How was this installed originally
```bash
gcloud auth login
gcloud config set project gitpod-core-dev
export ZONE=europe-west1-b
export PROJECT=gitpod-core-dev
gcloud container --project "${PROJECT}" clusters create prow   --zone "${ZONE}" --machine-type n1-standard-4 --num-nodes 2
gcloud iam service-accounts create prow-gcs-publisher
identifier="$(  gcloud iam service-accounts list --filter 'name:prow-gcs-publisher' --format 'value(email)' )"
gsutil mb gs://gitpod-prow-artifacts/ 
gsutil iam ch allUsers:objectViewer gs://gitpod-prow-artifacts
gsutil iam ch "serviceAccount:${identifier}:objectAdmin" gs://gitpod-prow-artifacts
gcloud iam service-accounts keys create --iam-account "${identifier}" service-account.json
kubectl apply -f prow.yaml 
kubectl -n test-pods create secret generic gcs-credentials --from-file=service-account.json 
rm service-account.json 
gsutil mb gs://gitpod-prow-tide/ 
gsutil iam ch allUsers:objectViewer gs://gitpod-prow-tide
gsutil iam ch "serviceAccount:${identifier}:objectAdmin" gs://gitpod-prow-tide
gsutil mb gs://gitpod-prow-statusreconciler/ 
gsutil iam ch allUsers:objectViewer gs://gitpod-prow-stausreconciler
gsutil iam ch allUsers:objectViewer gs://gitpod-prow-statusreconciler/
gsutil iam ch "serviceAccount:${identifier}:objectAdmin" gs://gitpod-prow-statusreconciler/
for i in $(ls prow/*.yaml); do kubectl apply -f $i; done
```