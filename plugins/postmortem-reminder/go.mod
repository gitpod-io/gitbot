module github.com/gitpod-io/gitbot/postmortem-reminder

go 1.16

replace k8s.io/client-go => k8s.io/client-go v0.21.1

replace github.com/gitpod-io/gitbot/common => ../common

require (
	github.com/sirupsen/logrus v1.8.1
	k8s.io/test-infra v0.0.0-20210802182419-bd4b5f60036c
)
