package main

import (
	"log"
	"os"

	"github.com/percona/percona-backup-mongodb/e2e-tests/pkg/tests/sharded"
)

type testTyp string

const (
	testsUnknown   testTyp = ""
	testsSharded   testTyp = "sharded"
	testsRemapping testTyp = "remapping"
	testsRS        testTyp = "rs"

	defaultMongoUser = "bcp"
	defaultMongoPass = "test1234"

	dockerURI = "tcp://docker-host:2375"
)

type bcpTyp string

const (
	bcpLogical  bcpTyp = "logical"
	bcpPhysical bcpTyp = "physical"
)

func main() {
	mUser := os.Getenv("BACKUP_USER")
	if mUser == "" {
		mUser = defaultMongoUser
	}
	mPass := os.Getenv("MONGO_PASS")
	if mPass == "" {
		mPass = defaultMongoPass
	}

	bcpT := bcpLogical
	if bcpTyp(os.Getenv("TESTS_BCP_TYPE")) == bcpPhysical {
		bcpT = bcpPhysical
	}
	log.Println("Backup Type:", bcpT)

	typ := testTyp(os.Getenv("TESTS_TYPE"))
	switch typ {
	case testsUnknown, testsSharded:
		runSharded(mUser, mPass, bcpT)
	case testsRS:
		runRS(mUser, mPass, bcpT)
	case testsRemapping:
		runRemapping(mUser, mPass)
	default:
		log.Fatalln("UNKNOWN TEST TYPE:", typ)
	}
}

func runRS(mUser, mPass string, bcpT bcpTyp) {
	allTheNetworks := "mongodb://" + mUser + ":" + mPass + "@rs101:27017/"
	tests := sharded.New(sharded.ClusterConf{
		Mongos:          allTheNetworks,
		Configsrv:       allTheNetworks,
		ConfigsrvRsName: "rs1",
		Shards: map[string]string{
			"rs1": allTheNetworks,
		},
		DockerURI:    dockerURI,
		PbmContainer: "pbmagent_rs101",
	})

	switch bcpT {
	case bcpPhysical:
		runPhysical(tests, testsRS)
	default:
		run(tests, testsRS)
	}
}

func runRemapping(mUser, mPass string) {
	allTheNetworksRS1 := "mongodb://" + mUser + ":" + mPass + "@rs101:27017/"
	allTheNetworksRS2 := "mongodb://" + mUser + ":" + mPass + "@rs201:27017/"
	tests := &sharded.RemappingEnvironment{
		Donor: sharded.New(sharded.ClusterConf{
			Mongos:          allTheNetworksRS1,
			Configsrv:       allTheNetworksRS1,
			ConfigsrvRsName: "rs1",
			Shards: map[string]string{
				"rs1": allTheNetworksRS1,
			},
			DockerURI:    dockerURI,
			PbmContainer: "pbmagent_rs101",
		}),
		Recipient: sharded.New(sharded.ClusterConf{
			Mongos:          allTheNetworksRS2,
			Configsrv:       allTheNetworksRS2,
			ConfigsrvRsName: "rs2",
			Shards: map[string]string{
				"rs2": allTheNetworksRS2,
			},
			DockerURI:    dockerURI,
			PbmContainer: "pbmagent_rs201",
		}),
		Remapping: map[string]string{"rs2": "rs1"},
	}
	runRemappingTests(tests)
}

func runSharded(mUser, mPass string, bcpT bcpTyp) {
	tests := sharded.New(sharded.ClusterConf{
		Mongos:          "mongodb://" + mUser + ":" + mPass + "@mongos:27017/",
		Configsrv:       "mongodb://" + mUser + ":" + mPass + "@cfg01:27017/",
		ConfigsrvRsName: "cfg",
		Shards: map[string]string{
			"rs1": "mongodb://" + mUser + ":" + mPass + "@rs101:27017/",
			"rs2": "mongodb://" + mUser + ":" + mPass + "@rs201:27017/",
		},
		DockerURI:    dockerURI,
		PbmContainer: "pbmagent_rs101",
	})

	switch bcpT {
	case bcpPhysical:
		runPhysical(tests, testsSharded)
	default:
		run(tests, testsSharded)
	}
}
