module github.com/gitpod-io/gitbot/common

go 1.16

replace k8s.io/client-go => k8s.io/client-go v0.21.1

require (
	k8s.io/apimachinery v0.21.2 // indirect
	k8s.io/test-infra v0.0.0-20210709075653-27c63cf722e7
)
