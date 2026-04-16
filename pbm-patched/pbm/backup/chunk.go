package backup

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

const chunkUnixFormat = "20060102150405"

func FormatChunkName(start, end primitive.Timestamp, cmp compress.CompressionType) string {
	return fmt.Sprintf("%s-%s.%s-%s%s",
		time.Unix(int64(start.T), 0).UTC().Format(chunkUnixFormat),
		strconv.Itoa(int(start.I)),
		time.Unix(int64(end.T), 0).UTC().Format(chunkUnixFormat),
		strconv.Itoa(int(end.I)),
		cmp.Suffix())
}

//nolint:nonamedreturns
func ParseChunkName(filename string) (
	start primitive.Timestamp,
	end primitive.Timestamp,
	comp compress.CompressionType,
	err error,
) {
	parts := strings.SplitN(filename, ".", 3)
	if len(parts) < 2 {
		err = errors.New("invalid format")
		return
	}

	start, err = parseChunkTime(parts[0])
	if err != nil {
		err = errors.Wrapf(err, "start time %q", parts[0])
		return
	}

	end, err = parseChunkTime(parts[1])
	if err != nil {
		err = errors.Wrapf(err, "end time %q", parts[0])
		return
	}

	if len(parts) == 3 {
		comp = compress.FileCompression(parts[2])
		if comp == "" {
			err = errors.Errorf("compression %q", parts[2])
			return
		}
	}

	return
}

func parseChunkTime(s string) (primitive.Timestamp, error) {
	var rv primitive.Timestamp

	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return rv, errors.New("invalid format")
	}

	t, err := time.Parse(chunkUnixFormat, parts[0])
	if err != nil {
		return rv, errors.Wrapf(err, "time %q", parts[0])
	}

	i, err := strconv.Atoi(parts[1])
	if err != nil {
		return rv, errors.Wrapf(err, "inc %q", parts[0])
	}

	rv.T = uint32(t.Unix())
	rv.I = uint32(i)
	return rv, nil
}
