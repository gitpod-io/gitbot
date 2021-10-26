module github.com/gitpod-io/gitbot/deployed-labeler

go 1.16

replace k8s.io/client-go => k8s.io/client-go v0.21.1

replace github.com/gitpod-io/gitbot/common => ../common

require (
	github.com/shurcooL/githubv4 v0.0.0-20191102174205-af46314aec7b
	github.com/sirupsen/logrus v1.8.1
	k8s.io/apimachinery v0.21.2 // indirect
	k8s.io/test-infra v0.0.0-20210709075653-27c63cf722e7
)
