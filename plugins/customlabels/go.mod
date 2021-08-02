module github.com/gitpod-io/gitbot/customlabels

replace k8s.io/client-go => k8s.io/client-go v0.21.1

replace github.com/gitpod-io/gitbot/common => ../common

go 1.16

require (
	github.com/gitpod-io/gitbot/common v0.0.0-00010101000000-000000000000
	github.com/sirupsen/logrus v1.8.1
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b
	k8s.io/apimachinery v0.21.2
	k8s.io/test-infra v0.0.0-20210728091535-0350d2f3dd7a
)
