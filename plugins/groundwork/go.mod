module github.com/gitpod-io/gitbot/groundwork

go 1.16

replace k8s.io/client-go => k8s.io/client-go v0.21.1

replace github.com/gitpod-io/gitbot/common => ../common

require (
	github.com/gitpod-io/gitbot/common v0.0.0-00010101000000-000000000000
	github.com/sirupsen/logrus v1.8.1
	k8s.io/test-infra v0.0.0-20210709075653-27c63cf722e7
	sigs.k8s.io/yaml v1.2.0
)
