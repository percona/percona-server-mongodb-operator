module github.com/percona/percona-server-mongodb-operator

go 1.16

require (
	github.com/Percona-Lab/percona-version-service/api v0.0.0-20200714141734-e9fed619b55c
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/go-logr/logr v0.1.0
	github.com/go-openapi/errors v0.19.6
	github.com/go-openapi/runtime v0.19.16
	github.com/go-openapi/strfmt v0.19.5
	github.com/go-openapi/swag v0.19.9
	github.com/go-openapi/validate v0.19.10
	github.com/hashicorp/go-version v1.2.0
	github.com/jetstack/cert-manager v0.15.1
	github.com/mitchellh/go-ps v1.0.0
	github.com/operator-framework/operator-sdk v0.17.2
	github.com/percona/percona-backup-mongodb v1.6.1-0.20211208103648-66bc6bfdc9ff
	github.com/pkg/errors v0.9.1
	github.com/robfig/cron/v3 v3.0.1
	github.com/sirupsen/logrus v1.7.0
	github.com/stretchr/testify v1.6.1
	github.com/timvaillancourt/go-mongodb-fixtures v0.0.0-20180517014041-bee1cce826fb // indirect
	github.com/timvaillancourt/go-mongodb-replset v0.0.0-20180529222116-173aaa3b66af
	github.com/valyala/fasthttp v1.17.0
	go.mongodb.org/mongo-driver v1.7.0
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/tools v0.0.0-20201028153306-37f0764111ff // indirect
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	honnef.co/go/tools v0.0.1-2020.1.5 // indirect
	k8s.io/api v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.5.2
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v14.2.0+incompatible // Required by OLM
	github.com/dgrijalva/jwt-go v3.2.0+incompatible => github.com/golang-jwt/jwt/v4 v4.2.0
	k8s.io/api => k8s.io/api v0.17.4 // Required by client-go
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.4 // Required by client-go
	k8s.io/client-go => k8s.io/client-go v0.17.4 // Required by prometheus-operator
)
