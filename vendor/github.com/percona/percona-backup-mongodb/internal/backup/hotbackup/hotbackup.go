package hotbackup

import (
	"errors"
	"os"
	"strings"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
)

var (
	ErrUnsupported   = errors.New("server does not support hot backup")
	ErrNotLocalhost  = errors.New("session must be direct session to localhost")
	ErrNotDirectConn = errors.New("session is not direct")
)

func checkHotBackup(session *mgo.Session) error {
	resp := struct {
		Commands map[string]interface{} `bson:"commands"`
	}{}
	err := session.Run(bson.D{{"listCommands", 1}}, &resp)
	if err != nil {
		return err
	}
	if _, ok := resp.Commands["createBackup"]; ok {
		return nil
	}
	return ErrUnsupported
}

func checkLocalhostSession(session *mgo.Session) error {
	// get system hostname
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	// check the server host == os.Hostname
	status := struct {
		Host string `bson:"host"`
	}{}
	err = session.Run(bson.D{{"serverStatus", 1}}, &status)
	if err != nil {
		return err
	}
	split := strings.SplitN(status.Host, ":", 2)
	if split[0] != hostname {
		return ErrNotLocalhost
	}

	// check connection is direct
	servers := session.LiveServers()
	if len(servers) != 1 {
		return ErrNotDirectConn
	}
	split = strings.SplitN(servers[0], ":", 2)
	for _, match := range []string{"127.0.0.1", "localhost", hostname} {
		if split[0] == match {
			return nil
		}
	}
	return ErrNotLocalhost
}
