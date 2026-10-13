package versionservice

import (
	"fmt"
	"os"
	"strings"

	v "github.com/hashicorp/go-version"
	"github.com/pkg/errors"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// passed version should have format "Major.Minor"
func CanUpgradeVersion(fcv, target string) bool {
	if fcv >= target {
		return false
	}

	switch fcv {
	case "3.6":
		return target == "4.0"
	case "4.0":
		return target == "4.2"
	case "4.2":
		return target == "4.4"
	case "4.4":
		return target == "5.0"
	case "5.0":
		return target == "6.0"
	case "6.0":
		return target == "7.0"
	case "7.0":
		return target == "8.0"
	default:
		return false
	}
}

type upgradeRequest struct {
	Ok         bool
	Apply      string
	NewVersion string
}

func MajorMinor(ver *v.Version) string {
	s := ver.Segments()

	if len(s) == 1 {
		s = append(s, 0)
	}

	return fmt.Sprintf("%d.%d", s[0], s[1])
}

func majorUpgradeRequested(cr *api.PerconaServerMongoDB, fcv string) (upgradeRequest, error) {
	if len(cr.Spec.UpgradeOptions.Apply) == 0 || api.OneOfUpgradeStrategy(string(cr.Spec.UpgradeOptions.Apply)) {
		return upgradeRequest{}, nil
	}

	apply := ""
	ver := string(cr.Spec.UpgradeOptions.Apply)

	applySp := strings.Split(string(cr.Spec.UpgradeOptions.Apply), "-")
	if len(applySp) > 1 && api.OneOfUpgradeStrategy(applySp[1]) {
		// if CR has "apply: 4.2-recommended"
		// 4.2 will go to version
		// recommended will go to apply
		apply = applySp[1]
		ver = applySp[0]
	}

	newVer, err := v.NewSemver(ver)
	if err != nil {
		return upgradeRequest{}, errors.Wrapf(err, "parse version %s from spec.upgradeOptions.apply", ver)
	}

	if len(cr.Status.MongoVersion) == 0 {
		// means cluster is starting
		// so we do not need to check is we can upgrade
		return upgradeRequest{Ok: true, Apply: apply, NewVersion: ver}, nil
	}

	mongoVer, err := v.NewSemver(cr.Status.MongoVersion)
	if err != nil {
		return upgradeRequest{}, errors.Wrapf(err, "parse version %s from status.mongoVersion", cr.Status.MongoVersion)
	}

	newMM := MajorMinor(newVer)
	mongoMM := MajorMinor(mongoVer)

	if newMM > mongoMM {
		if !CanUpgradeVersion(fcv, newMM) {
			return upgradeRequest{}, errors.Errorf("can't upgrade to %s with FCV set to %s", ver, fcv)
		}

		return upgradeRequest{Ok: true, Apply: apply, NewVersion: ver}, nil
	}

	if newMM < mongoMM {
		if newMM != fcv {
			return upgradeRequest{}, errors.Errorf("can't upgrade to %s with FCV set to %s", ver, fcv)
		}

		return upgradeRequest{Ok: true, Apply: apply, NewVersion: ver}, nil
	}

	return upgradeRequest{}, nil
}

func TelemetryEnabled() bool {
	value, ok := os.LookupEnv("DISABLE_TELEMETRY")
	if ok {
		return value != "true"
	}
	return true
}

func UpgradeEnabled(cr *api.PerconaServerMongoDB) bool {
	return cr.Spec.UpgradeOptions.Apply.Lower() != api.UpgradeStrategyNever &&
		cr.Spec.UpgradeOptions.Apply.Lower() != api.UpgradeStrategyDisabled
}
