module github.com/gitpod-io/gitbot/observer

go 1.16

replace k8s.io/client-go => k8s.io/client-go v0.21.1

replace github.com/gitpod-io/gitbot/common => ../common

require (
	cloud.google.com/go/bigquery v1.19.0
	github.com/gitpod-io/gitbot/common v0.0.0-00010101000000-000000000000
	github.com/shurcooL/githubv4 v0.0.0-20191102174205-af46314aec7b
	github.com/sirupsen/logrus v1.8.1
	google.golang.org/api v0.49.0
	k8s.io/test-infra v0.0.0-20210709075653-27c63cf722e7
	sigs.k8s.io/yaml v1.2.0
)
