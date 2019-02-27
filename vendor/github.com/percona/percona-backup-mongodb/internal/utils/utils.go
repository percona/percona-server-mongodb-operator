package utils

import (
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/alecthomas/kingpin"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

func LoadOptionsFromFile(filename string, c *kingpin.ParseContext, opts interface{}) error {
	filename = Expand(filename)

	buf, err := ioutil.ReadFile(filepath.Clean(filename))
	if err != nil {
		return errors.Wrap(err, "cannot load configuration from file")
	}
	if err = yaml.Unmarshal(buf, opts); err != nil {
		return errors.Wrapf(err, "cannot unmarshal yaml file %s: %s", filename, err)
	}
	s := reflect.ValueOf(opts).Elem()
	walk(c, s)

	return nil
}

func walk(c *kingpin.ParseContext, opts reflect.Value) {
	// Overwrite values from the config with the values from the command line (kingpin.ParseContext)
	t := opts.Type()

	for i := 0; i < opts.NumField(); i++ {
		f := opts.Field(i)
		flagName := t.Field(i).Tag.Get("kingpin")
		if f.Kind() == reflect.Struct {
			walk(c, f)
		}

		if f.CanSet() && flagName != "" {
			argFromContext := getArg(c, flagName)
			if argFromContext != "" {
				switch f.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					if iv, err := strconv.Atoi(argFromContext); err == nil {
						f.SetInt(int64(iv))
					}
				case reflect.Bool:
					if bv, err := strconv.ParseBool(argFromContext); err == nil {
						f.SetBool(bv)
					}
				case reflect.String:
					f.SetString(argFromContext)
				}
			}
		}
	}

}

func getArg(c *kingpin.ParseContext, argName string) string {
	for _, v := range c.Elements {
		if value, ok := v.Clause.(*kingpin.FlagClause); ok {
			if value.Model().Name == argName {
				return *v.Value
			}
		}
	}
	return ""
}

func Expand(path string) string {
	path = os.ExpandEnv(path)
	dir := os.Getenv("HOME")
	usr, err := user.Current()
	if err == nil {
		dir = usr.HomeDir
	}
	if path == "~" {
		return dir
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(dir, path[2:])
	}
	return path
}
