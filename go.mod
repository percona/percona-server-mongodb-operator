module github.com/percona/percona-server-mongodb-operator

go 1.13

require (
	github.com/Percona-Lab/percona-version-service/api v0.0.0-20200714141734-e9fed619b55c
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/aws/aws-sdk-go v1.31.13 // indirect
	github.com/go-logr/logr v0.1.0
	github.com/go-openapi/errors v0.19.6
	github.com/go-openapi/runtime v0.19.16
	github.com/go-openapi/strfmt v0.19.5
	github.com/go-openapi/swag v0.19.9
	github.com/go-openapi/validate v0.19.10
	github.com/golang/mock v1.3.1
	github.com/google/go-cmp v0.5.1 // indirect
	github.com/hashicorp/go-version v1.2.0
	github.com/jetstack/cert-manager v0.15.1
	github.com/mitchellh/go-ps v1.0.0
	github.com/operator-framework/operator-sdk v0.17.2
	github.com/percona/percona-backup-mongodb v1.4.1
	github.com/percona/pmgo v0.0.0-20171205120904-497d06e28f91
	github.com/pkg/errors v0.9.1
	github.com/robfig/cron/v3 v3.0.1
	github.com/sirupsen/logrus v1.7.0
	github.com/stretchr/testify v1.6.1
	github.com/timvaillancourt/go-mongodb-fixtures v0.0.0-20180517014041-bee1cce826fb // indirect
	github.com/timvaillancourt/go-mongodb-replset v0.0.0-20180529222116-173aaa3b66af
	github.com/valyala/fasthttp v1.17.0
	go.mongodb.org/mongo-driver v1.3.4
	golang.org/x/tools v0.0.0-20201028153306-37f0764111ff // indirect
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	gopkg.in/tomb.v2 v2.0.0-20161208151619-d5d1b5820637 // indirect
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
