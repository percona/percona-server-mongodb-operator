# Percona MongoDB Go driver

[![](https://godoc.org/github.com/percona/toolkit-go/pmgo?status.svg)](https://godoc.org/github.com/percona/toolkit-go/pmgo)

**WIP** NOT ALL INTERFACES HAVE BEEN IMPLEMENTED YET  

This is just a collection of interfaces around the structures in mgo, ([Rich MongoDB driver for Go](https://labix.org/mgo)) to be able to mock methods in the driver.  

The motivation for this package is that there are certain things, like errors, that cannot be tested/reproduced using a real db connection.  
Also, for some of our tests we need very specific MongoDB configuration. Tests for some parts of our code need 2 replicas, config and mongo**s** servers and that's not easily reproducible in all CI environments.

## How to use it

This package is almost a drop-in replacement with the exception that you need to use the `Dialer` interface.

```go
package main

import (
    "fmt"
    "log"

    "github.com/percona/pmgo"
    "gopkg.in/mgo.v2/bson"
)

type User struct {
    ID   int    `bson:"id"`
    Name string `bson:"name"`
}

func main() {
    dialer := pmgo.NewDialer()
    session, err := dialer.Dial("localhost")
    if err != nil {
        print(err)
        return
    }

    user, err := getUser(session, 1)
    if err != nil {
        log.Printf("error reading the user from the db: %s", err.Error())
        return
    }
    fmt.Printf("User: %+v\n", user)

}

func getUser(session pmgo.SessionManager, id int) (*User, error) {
    var user User
    err := session.DB("test").C("testc").Find(bson.M{"id": id}).One(&user)

    if err != nil {
        return nil, err
    }

    return &user, nil
}
```

### How to write unitary tests (mocking interfaces)

```go
package main

import (
    "fmt"
    "io/ioutil"
    "os"
    "reflect"
    "testing"

    "gopkg.in/mgo.v2/bson"

    "github.com/golang/mock/gomock"
    "github.com/percona/pmgo"
    "github.com/percona/pmgo/pmgomock"
)

var Server pmgo.DBTestServer

func TestGetUser(t *testing.T) {

    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    user := User{
        ID:   1,
        Name: "Zapp Brannigan",
    }

    // Mock up a database, session, collection and a query and set
    // expected/returned values for each type
    query := pmgomock.NewMockQueryManager(ctrl)
    query.EXPECT().One(gomock.Any()).SetArg(0, user).Return(nil)

    collection := pmgomock.NewMockCollectionManager(ctrl)
    collection.EXPECT().Find(bson.M{"id": 1}).Return(query)

    database := pmgomock.NewMockDatabaseManager(ctrl)
    database.EXPECT().C("testc").Return(collection)

    session := pmgomock.NewMockSessionManager(ctrl)
    session.EXPECT().DB("test").Return(database)

    // Call the function we want to test. It will use the mocked interfaces
    readUser, err := getUser(session, 1)
   
     if err != nil {
         t.Errorf("getUser returned an error: %s\n", err.Error())
     }

     if !reflect.DeepEqual(*readUser, user) {
         t.Errorf("Users don't match. Got %+v, want %+v\n", readUser, user)
     }
}
```
  
### How to write integration tests  
A not so well known testing method is the use of mgo's dbtest server.  
dbtest starts a new MongoDB instance (mongo binary must be in the path), using a temporary directory as dbpath 
and then on Stop() it will clean all testing data.  
pmgo also has interfaces for `dbtest.DBServer` to use in integration tests:  

```go
func TestIntegration(t *testing.T) {
    setup()

    readUser, err := getUser(Server.Session(), 1)
    if err != nil {
        t.Errorf("getUser returned an error: %s\n", err.Error())
    }

    if !reflect.DeepEqual(*readUser, mockUser()) {
        t.Errorf("Users don't match. Got %+v, want %+v\n", readUser, mockUser())
    }

    tearDown()
}

func setup() {
    os.Setenv("CHECK_SESSIONS", "0")
    tempDir, _ := ioutil.TempDir("", "testing")
    Server = pmgo.NewDBServer()
    Server.SetPath(tempDir)

    session := Server.Session()
    // load some fake data into the db
    session.DB("test").C("testc").Insert(mockUser())
}

func mockUser() User {
    return User{
                 ID:   1,
                 Name: "Zapp Brannigan",
               }

}

func tearDown() {
    Server.Session().Close()
    Server.Session().DB("samples").DropDatabase()
    Server.Stop()
}
```


## Generating new mocks
If you update a file to add more functions, you can create new mocks by running:  
```
mockgen -source <path>/pmgo/collection.go -destination=<path>/pmgo/pmgomock/collection.go -package pmgomock -imports ".=github.com/percona/pmgo"
```

