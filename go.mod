module github.com/percona/percona-server-mongodb-operator

go 1.16

require (
	github.com/Percona-Lab/percona-version-service/api v0.0.0-20220117163507-01f7e978dde7
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/go-logr/logr v1.2.2
	github.com/go-openapi/errors v0.20.1
	github.com/go-openapi/runtime v0.21.0
	github.com/go-openapi/strfmt v0.21.1
	github.com/go-openapi/swag v0.19.15
	github.com/go-openapi/validate v0.20.3
	github.com/hashicorp/go-version v1.4.0
	github.com/jetstack/cert-manager v1.6.1
	github.com/mitchellh/go-ps v1.0.0
	//github.com/operator-framework/operator-sdk v0.17.2
	github.com/operator-framework/operator-lib v0.10.0
	github.com/percona/percona-backup-mongodb v1.6.1-0.20220110120847-2c3b83a6d7b4
	github.com/pkg/errors v0.9.1
	github.com/robfig/cron/v3 v3.0.1
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/timvaillancourt/go-mongodb-fixtures v0.0.0-20180517014041-bee1cce826fb // indirect
	github.com/timvaillancourt/go-mongodb-replset v0.0.0-20180529222116-173aaa3b66af
	github.com/valyala/fasthttp v1.32.0
	go.mongodb.org/mongo-driver v1.8.3
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	k8s.io/api v0.23.2
	k8s.io/apimachinery v0.23.2
	k8s.io/client-go v0.23.2
	sigs.k8s.io/controller-runtime v0.11.0
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v14.2.0+incompatible // Required by OLM
	github.com/dgrijalva/jwt-go => github.com/golang-jwt/jwt/v4 v4.2.0
)
