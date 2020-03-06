package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/minio/minio-go"
	"github.com/percona/percona-backup-mongodb/pbm"
	"gopkg.in/yaml.v2"

	"github.com/percona/percona-backup-mongodb/e2e-tests/pkg/tests/sharded"
)

func main() {
	tests := sharded.New(sharded.ClusterConf{
		Mongos:          "mongodb://dba:test1234@mongos:27017/",
		Configsrv:       "mongodb://dba:test1234@cfg01:27017/",
		ConfigsrvRsName: "cfg",
		Shards: map[string]string{
			"rs1": "mongodb://dba:test1234@rs101:27017/",
			"rs2": "mongodb://dba:test1234@rs201:27017/",
		},
		DockerSocket: "unix:///var/run/docker.sock",
	})

	flushStore("/etc/pbm/aws.yaml")
	tests.ApplyConfig("/etc/pbm/aws.yaml")

	tests.DeleteBallast()
	tests.GenerateBallastData(1e5)

	printStart("Basic Backup & Restore AWS S3")
	tests.BackupAndRestore()
	printDone("Basic Backup & Restore AWS S3")

	flushStore("/etc/pbm/gcs.yaml")
	tests.ApplyConfig("/etc/pbm/gcs.yaml")
	log.Println("Waiting for the new storage to resync")
	time.Sleep(time.Second * 5)

	printStart("Basic Backup & Restore GCS")
	tests.BackupAndRestore()
	printDone("Basic Backup & Restore GCS")

	flushStore("/etc/pbm/minio.yaml")
	tests.ApplyConfig("/etc/pbm/minio.yaml")
	log.Println("Waiting for the new storage to resync")
	time.Sleep(time.Second * 5)

	printStart("Basic Backup & Restore Minio")
	tests.BackupAndRestore()
	printDone("Basic Backup & Restore Minio")

	printStart("Backup Data Bounds Check")
	tests.BackupBoundsCheck()
	printDone("Backup Data Bounds Check")

	printStart("Restart agents during the backup")
	tests.RestartAgents()
	printDone("Restart agents during the backup")

	printStart("Cut network during the backup")
	tests.NetworkCut()
	printDone("Cut network during the backup")

	cVersion := version.Must(version.NewVersion(tests.ServerVersion()))
	v42 := version.Must(version.NewVersion("4.2"))
	if cVersion.GreaterThanOrEqual(v42) {
		printStart("Distributed Transactions backup")
		tests.DistributedTransactions()
		printDone("Distributed Transactions backup")
	}

	printStart("Clock Skew Tests")
	tests.ClockSkew()
	printDone("Clock Skew Tests")
}

func printStart(name string) {
	log.Printf("[START] ======== %s ========\n", name)
}
func printDone(name string) {
	log.Printf("[DONE] ======== %s ========\n", name)
}

const awsurl = "s3.amazonaws.com"

func flushStore(conf string) {
	buf, err := ioutil.ReadFile(conf)
	if err != nil {
		log.Fatalln("Error: unable to read config file:", err)
	}

	var cfg pbm.Config
	err = yaml.UnmarshalStrict(buf, &cfg)
	if err != nil {
		log.Fatalln("Error: unmarshal yaml:", err)
	}

	stg := cfg.Storage

	endopintURL := awsurl
	if stg.S3.EndpointURL != "" {
		eu, err := url.Parse(stg.S3.EndpointURL)
		if err != nil {
			log.Fatalln("Error: parse EndpointURL:", err)
		}
		endopintURL = eu.Host
	}

	log.Println("Flushing store", endopintURL, stg.S3.Bucket, stg.S3.Prefix)

	mc, err := minio.NewWithRegion(endopintURL, stg.S3.Credentials.AccessKeyID, stg.S3.Credentials.SecretAccessKey, false, stg.S3.Region)
	if err != nil {
		log.Fatalln("Error: NewWithRegion:", err)
	}

	objectsCh := make(chan string)

	go func() {
		defer close(objectsCh)
		for object := range mc.ListObjects(stg.S3.Bucket, stg.S3.Prefix, true, nil) {
			if object.Err != nil {
				log.Fatalln(object.Err)
			}
			objectsCh <- object.Key
		}
	}()

	for rErr := range mc.RemoveObjects(stg.S3.Bucket, objectsCh) {
		fmt.Println("Error detected during deletion: ", rErr)
	}
}
