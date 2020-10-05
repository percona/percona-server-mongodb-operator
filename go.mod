module github.com/percona/percona-server-mongodb-operator

go 1.13

require (
	github.com/aws/aws-sdk-go v1.31.13 // indirect
	github.com/go-logr/logr v0.1.0
	github.com/google/go-cmp v0.5.1 // indirect
	github.com/hashicorp/go-version v1.2.0
	github.com/jetstack/cert-manager v0.15.1
	github.com/operator-framework/operator-sdk v0.17.1
	github.com/percona/percona-backup-mongodb v1.2.0
	github.com/pkg/errors v0.9.1
	github.com/robfig/cron/v3 v3.0.1
	github.com/stretchr/testify v1.5.1
	go.mongodb.org/mongo-driver v1.3.4
	golang.org/x/tools v0.0.0-20200914163123-ea50a3c84940 // indirect
	honnef.co/go/tools v0.0.1-2020.1.5 // indirect
	k8s.io/api v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.5.2
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	k8s.io/api => k8s.io/api v0.17.4 // Required by client-go
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.4 // Required by client-go
	k8s.io/client-go => k8s.io/client-go v0.17.4 // Required by prometheus-operator
)
