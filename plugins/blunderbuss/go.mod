module github.com/gitpod-io/gitbot/blunderbuss

replace k8s.io/client-go => k8s.io/client-go v0.21.1

replace github.com/gitpod-io/gitbot/common => ../common

go 1.16

require (
	github.com/shurcooL/githubv4 v0.0.0-20191102174205-af46314aec7b
	github.com/sirupsen/logrus v1.8.1
	k8s.io/apimachinery v0.21.2
	k8s.io/test-infra v0.0.0-20210728091535-0350d2f3dd7a
)
