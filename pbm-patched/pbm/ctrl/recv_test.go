package ctrl

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
)

var (
	mClient    *mongo.Client
	connClient connect.Client
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	mongodbContainer, err := mongodb.Run(ctx, "perconalab/percona-server-mongodb:8.0.4-multi")
	if err != nil {
		log.Fatalf("error while creating mongo test container: %v", err)
	}
	connStr, err := mongodbContainer.ConnectionString(ctx)
	if err != nil {
		log.Fatalf("conn string error: %v", err)
	}
	mClient, err = mongo.Connect(ctx, options.Client().ApplyURI(connStr))
	if err != nil {
		log.Fatalf("mongo client connect error: %v", err)
	}

	connClient = connect.UnsafeClient(mClient)

	code := m.Run()

	err = mClient.Disconnect(ctx)
	if err != nil {
		log.Fatalf("mongo client disconnect error: %v", err)
	}
	if err := testcontainers.TerminateContainer(mongodbContainer); err != nil {
		log.Fatalf("failed to terminate container: %s", err)
	}

	os.Exit(code)
}

func TestListenCmd(t *testing.T) {
	ctx := context.Background()
	coll := connClient.CmdStreamCollection()

	testdata := [][]struct {
		cmd    string
		offset int
	}{
		{
			{"restore", 0},
			{"backup", 1},
		},

		{
			{"backup", 0},
			{"backup", 0},
		},
		{
			{"resync", 0},
			{"backup", 0},
			{"restore", 0},
			{"backup", 1},
			{"restore", 1},
			{"resync", 2},
			{"backup", 2},
			{"restore", 2},
		},
	}

	for index, entries := range testdata {
		_ = coll.Drop(ctx)

		stopCh := make(chan struct{})
		cmdC, errC := ListenCmd(ctx, connClient, stopCh)

		partTS := time.Now().UTC().Unix()

		var commands []struct {
			cmd string
			ts  int64
		}
		for _, t := range entries {
			commands = append(commands, struct {
				cmd string
				ts  int64
			}{t.cmd, partTS + int64(t.offset)})
		}

		for _, e := range commands {
			doc := bson.D{{"_id", primitive.NewObjectID()}, {"cmd", e.cmd}, {"ts", e.ts}}
			if _, err := coll.InsertOne(ctx, doc); err != nil {
				t.Fatalf("insert %s@%d: %v", e.cmd, e.ts, err)
			}
		}

		want := make(map[string]struct{})
		for _, e := range commands {
			want[fmt.Sprintf("%d/%s", e.ts, e.cmd)] = struct{}{}
		}

		idle := 3 * time.Second
		timer := time.NewTimer(idle)
		got := make(map[string]struct{})

		func() {
			for {
				select {
				case cmd := <-cmdC:
					key := fmt.Sprintf("%d/%s", cmd.TS, string(cmd.Cmd))
					got[key] = struct{}{}
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(idle)
				case err := <-errC:
					t.Fatalf("listener error: %v", err)
				case <-timer.C:
					return
				}
			}
		}()

		close(stopCh)

		if len(got) != len(want) {
			t.Errorf("part %d: expected %d unique commands, got %d", index+1, len(want), len(got))
		}

		for key := range want {
			if _, ok := got[key]; !ok {
				t.Errorf("part %d: missing expected command: %s", index+1, key)
			}
		}

		for key := range got {
			if _, ok := want[key]; !ok {
				t.Errorf("part %d: unexpected extra command: %s", index+1, key)
			}
		}
	}
}

func TestCheckDuplicateCmd(t *testing.T) {
	c1 := Cmd{Cmd: "backup", TS: 100}
	c2 := Cmd{Cmd: "restore", TS: 100}
	c3 := Cmd{Cmd: "backup", TS: 200}

	buckets := map[int64][]Cmd{
		100: {c1},
	}

	tests := []struct {
		name     string
		cmd      Cmd
		expected bool
	}{
		{"first command, new bucket", c3, false},
		{"different cmd, same ts", c2, false},
		{"exact duplicate", c1, true},
	}

	for _, tt := range tests {
		if got := checkDuplicateCmd(buckets, tt.cmd); got != tt.expected {
			t.Errorf("%s: want %v, got %v", tt.name, tt.expected, got)
		}
	}
}

func TestCleanupOldCmdBuckets(t *testing.T) {
	buckets := map[int64][]Cmd{
		90:  {{Cmd: "backup", TS: 90}},
		100: {{Cmd: "backup", TS: 100}},
		110: {{Cmd: "backup", TS: 110}},
	}

	cleanupOldCmdBuckets(buckets, 100)

	if _, ok := buckets[90]; ok {
		t.Errorf("bucket 90 should have been deleted")
	}
	if _, ok := buckets[100]; !ok {
		t.Errorf("bucket 100 should be kept (ts == lastTS)")
	}
	if _, ok := buckets[110]; !ok {
		t.Errorf("bucket 110 should be kept (ts > lastTS)")
	}
}
