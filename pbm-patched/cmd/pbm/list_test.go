package main

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/oplog"
)

func Test_splitByBaseSnapshot(t *testing.T) {
	tl := oplog.Timeline{Start: 3, End: 7}

	t.Run("lastWrite is nil", func(t *testing.T) {
		lastWrite := primitive.Timestamp{}
		got := splitByBaseSnapshot(lastWrite, tl)

		want := []pitrRange{
			{Range: tl, NoBaseSnapshot: true},
		}

		check(t, got, want)
	})

	t.Run("lastWrite > tl.End", func(t *testing.T) {
		lastWrite := primitive.Timestamp{T: tl.End + 1}
		got := splitByBaseSnapshot(lastWrite, tl)

		want := []pitrRange{
			{Range: tl, NoBaseSnapshot: true},
		}

		check(t, got, want)
	})

	t.Run("lastWrite = tl.End", func(t *testing.T) {
		lastWrite := primitive.Timestamp{T: tl.End}
		got := splitByBaseSnapshot(lastWrite, tl)

		want := []pitrRange{
			{Range: tl, NoBaseSnapshot: true},
		}

		check(t, got, want)
	})

	t.Run("lastWrite < tl.Start", func(t *testing.T) {
		lastWrite := primitive.Timestamp{T: tl.Start - 1}
		got := splitByBaseSnapshot(lastWrite, tl)

		want := []pitrRange{
			{Range: tl, NoBaseSnapshot: true},
		}

		check(t, got, want)
	})

	t.Run("lastWrite = tl.Start", func(t *testing.T) {
		lastWrite := primitive.Timestamp{T: tl.Start}
		got := splitByBaseSnapshot(lastWrite, tl)

		want := []pitrRange{
			{
				Range: oplog.Timeline{
					Start: lastWrite.T + 1,
					End:   tl.End,
				},
				NoBaseSnapshot: false,
			},
		}

		check(t, got, want)
	})

	t.Run("tl.Start < lastWrite < tl.End", func(t *testing.T) {
		lastWrite := primitive.Timestamp{T: 5}
		got := splitByBaseSnapshot(lastWrite, tl)

		want := []pitrRange{
			{
				Range: oplog.Timeline{
					Start: tl.Start,
					End:   lastWrite.T,
				},
				NoBaseSnapshot: true,
			},
			{
				Range: oplog.Timeline{
					Start: lastWrite.T + 1,
					End:   tl.End,
				},
				NoBaseSnapshot: false,
			},
		}

		check(t, got, want)
	})
}

func check(t *testing.T, got, want []pitrRange) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got: %v, want: %v", got, want)
	}
}
