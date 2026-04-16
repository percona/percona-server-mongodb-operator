package oplog

import (
	"fmt"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestPITRTimelines(t *testing.T) {
	chunks := []OplogChunk{
		{
			StartTS: primitive.Timestamp{1, 0},
			EndTS:   primitive.Timestamp{10, 0},
		},
		{
			StartTS: primitive.Timestamp{10, 0},
			EndTS:   primitive.Timestamp{10, 0},
		},
		{
			StartTS: primitive.Timestamp{10, 0},
			EndTS:   primitive.Timestamp{20, 0},
		},
		{
			StartTS: primitive.Timestamp{30, 0},
			EndTS:   primitive.Timestamp{40, 0},
		},
		{
			StartTS: primitive.Timestamp{666, 0},
			EndTS:   primitive.Timestamp{779, 0},
		},
		{
			StartTS: primitive.Timestamp{777, 0},
			EndTS:   primitive.Timestamp{988, 0},
		},
		{
			StartTS: primitive.Timestamp{888, 0},
			EndTS:   primitive.Timestamp{1000, 0},
		},
	}

	tlines := []Timeline{
		{
			Start: 1,
			End:   20,
		},
		{
			Start: 30,
			End:   40,
		},
		{
			Start: 666,
			End:   1000,
		},
	}

	glines := gettimelines(chunks)
	if len(tlines) != len(glines) {
		t.Fatalf("wrong timelines, exepct [%d] %v, got [%d] %v", len(tlines), tlines, len(glines), glines)
	}
	for i, gl := range glines {
		if tlines[i] != gl {
			t.Errorf("wrong timeline %d, exepct %v, got %v", i, tlines[i], gl)
		}
	}
}

func TestPITRMergeTimelines(t *testing.T) {
	tests := []struct {
		name   string
		tl     [][]Timeline
		expect []Timeline
	}{
		{
			name:   "nothing",
			tl:     [][]Timeline{},
			expect: []Timeline{},
		},
		{
			name: "empy set",
			tl: [][]Timeline{
				{},
			},
			expect: []Timeline{},
		},
		{
			name: "no match",
			tl: [][]Timeline{
				{
					{Start: 3, End: 6},
					{Start: 14, End: 19},
					{Start: 20, End: 42},
				},
				{
					{Start: 1, End: 3},
					{Start: 6, End: 14},
					{Start: 50, End: 55},
				},
				{
					{Start: 7, End: 10},
					{Start: 12, End: 19},
					{Start: 20, End: 26},
					{Start: 27, End: 60},
				},
			},
			expect: []Timeline{},
		},
		{
			name: "no match2",
			tl: [][]Timeline{
				{
					{Start: 1, End: 5},
					{Start: 8, End: 13},
				},
				{
					{Start: 6, End: 7},
				},
			},
			expect: []Timeline{},
		},
		{
			name: "no match3",
			tl: [][]Timeline{
				{
					{Start: 1, End: 5},
					{Start: 8, End: 13},
				},
				{
					{Start: 5, End: 8},
				},
			},
			expect: []Timeline{},
		},
		{
			name: "some empty",
			tl: [][]Timeline{
				{},
				{
					{Start: 4, End: 8},
				},
			},
			expect: []Timeline{{Start: 4, End: 8}},
		},
		{
			name: "no gaps",
			tl: [][]Timeline{
				{
					{Start: 1, End: 5},
				},
				{
					{Start: 4, End: 8},
				},
			},
			expect: []Timeline{{Start: 4, End: 5}},
		},
		{
			name: "no gaps2",
			tl: [][]Timeline{
				{
					{Start: 4, End: 8},
				},
				{
					{Start: 1, End: 5},
				},
			},
			expect: []Timeline{{Start: 4, End: 5}},
		},
		{
			name: "no gaps3",
			tl: [][]Timeline{
				{
					{Start: 1, End: 8},
				},
				{
					{Start: 1, End: 5},
				},
			},
			expect: []Timeline{{Start: 1, End: 5}},
		},
		{
			name: "overlaps",
			tl: [][]Timeline{
				{
					{Start: 2, End: 6},
					{Start: 8, End: 12},
					{Start: 13, End: 15},
				},
				{
					{Start: 1, End: 4},
					{Start: 9, End: 14},
				},
				{
					{Start: 3, End: 7},
					{Start: 8, End: 11},
					{Start: 12, End: 14},
				},
				{
					{Start: 2, End: 9},
					{Start: 10, End: 17},
				},
				{
					{Start: 1, End: 5},
					{Start: 6, End: 14},
					{Start: 15, End: 19},
				},
			},
			expect: []Timeline{
				{Start: 3, End: 4},
				{Start: 10, End: 11},
				{Start: 13, End: 14},
			},
		},
		{
			name: "all match",
			tl: [][]Timeline{
				{
					{Start: 3, End: 6},
					{Start: 14, End: 19},
					{Start: 19, End: 42},
				},
				{
					{Start: 3, End: 6},
					{Start: 14, End: 19},
					{Start: 19, End: 42},
				},
				{
					{Start: 3, End: 6},
					{Start: 14, End: 19},
					{Start: 19, End: 42},
				},
				{
					{Start: 3, End: 6},
					{Start: 14, End: 19},
					{Start: 19, End: 42},
				},
			},
			expect: []Timeline{
				{Start: 3, End: 6},
				{Start: 14, End: 19},
				{Start: 19, End: 42},
			},
		},
		{
			name: "partly overlap",
			tl: [][]Timeline{
				{
					{Start: 3, End: 8},
					{Start: 14, End: 19},
					{Start: 21, End: 42},
				},
				{
					{Start: 1, End: 3},
					{Start: 4, End: 7},
					{Start: 19, End: 36},
				},
				{
					{Start: 5, End: 8},
					{Start: 14, End: 19},
					{Start: 20, End: 42},
				},
			},
			expect: []Timeline{
				{Start: 5, End: 7},
				{Start: 21, End: 36},
			},
		},
		{
			name: "partly overlap2",
			tl: [][]Timeline{
				{
					{Start: 1, End: 4},
					{Start: 7, End: 11},
					{Start: 16, End: 20},
				},
				{
					{Start: 3, End: 12},
					{Start: 15, End: 17},
				},
				{
					{Start: 1, End: 12},
					{Start: 16, End: 18},
				},
			},
			expect: []Timeline{
				{Start: 3, End: 4},
				{Start: 7, End: 11},
				{Start: 16, End: 17},
			},
		},
		{
			name: "redundant chunks",
			tl: [][]Timeline{
				{
					{Start: 3, End: 6},
					{Start: 14, End: 19},
					{Start: 19, End: 40},
					{Start: 42, End: 100500},
				},
				{
					{Start: 2, End: 7},
					{Start: 7, End: 8},
					{Start: 8, End: 10},
					{Start: 14, End: 20},
					{Start: 20, End: 30},
				},
				{
					{Start: 1, End: 5},
					{Start: 13, End: 19},
					{Start: 20, End: 30},
				},
			},
			expect: []Timeline{
				{Start: 3, End: 5},
				{Start: 14, End: 19},
				{Start: 20, End: 30},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := MergeTimelines(test.tl...)
			if len(test.expect) != len(got) {
				t.Fatalf("wrong timelines, exepct <%d> %v, got <%d> %v",
					len(test.expect), printttl(test.expect...), len(got), printttl(got...))
			}
			for i, gl := range got {
				if test.expect[i] != gl {
					t.Errorf("wrong timeline %d, exepct %v, got %v",
						i, printttl(test.expect[i]), printttl(gl))
				}
			}
		})
	}
}

func BenchmarkMergeTimeliens(b *testing.B) {
	tl := [][]Timeline{
		{
			{Start: 3, End: 8},
			{Start: 14, End: 19},
			{Start: 21, End: 42},
		},
		{
			{Start: 1, End: 3},
			{Start: 4, End: 7},
			{Start: 19, End: 36},
		},
		{
			{Start: 5, End: 8},
			{Start: 14, End: 19},
			{Start: 20, End: 42},
		},
		{
			{Start: 3, End: 6},
			{Start: 14, End: 19},
			{Start: 19, End: 40},
			{Start: 42, End: 100500},
		},
		{
			{Start: 2, End: 7},
			{Start: 7, End: 8},
			{Start: 8, End: 10},
			{Start: 14, End: 20},
			{Start: 20, End: 30},
			{Start: 31, End: 40},
			{Start: 41, End: 50},
			{Start: 51, End: 60},
		},
		{
			{Start: 1, End: 5},
			{Start: 13, End: 19},
			{Start: 20, End: 30},
		},
	}
	for i := 0; i < b.N; i++ {
		MergeTimelines(tl...)
	}
}

func printttl(tlns ...Timeline) string {
	ret := []string{}
	for _, t := range tlns {
		ret = append(ret, fmt.Sprintf("[%v - %v]", t.Start, t.End))
	}

	return strings.Join(ret, ", ")
}
