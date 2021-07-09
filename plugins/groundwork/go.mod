module github.com/gitpod-io/gitbot/groundwork

go 1.16

replace k8s.io/client-go => k8s.io/client-go v0.21.1

require (
	github.com/sirupsen/logrus v1.8.1
	k8s.io/apimachinery v0.21.2
	k8s.io/test-infra v0.0.0-20210709075653-27c63cf722e7
)
