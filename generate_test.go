package main

import "testing"

func TestDedupe(t *testing.T) {
	for name, tt := range map[string]struct {
		in      []rawSponsor
		want    int
		wantSrc string // source of the first result, if set
	}{
		"same id": {
			in:   []rawSponsor{{id: "Foo", monthlyUSD: 1}, {id: "foo", monthlyUSD: 5}},
			want: 1,
		},
		"same website across sources keeps highest": {
			in: []rawSponsor{
				{id: "ethantroy", source: "github", website: "https://ethantroy.dev", monthlyUSD: 5},
				{id: "ethan-troy", source: "opencollective", website: "http://www.EthanTroy.dev/", monthlyUSD: 10},
			},
			want:    1,
			wantSrc: "opencollective",
		},
		"github profile fallbacks are not merged": {
			in: []rawSponsor{
				{id: "a", website: "https://github.com/a"},
				{id: "b", website: "https://github.com/b"},
			},
			want: 2,
		},
		"empty websites are not merged": {
			in:   []rawSponsor{{id: "a"}, {id: "b"}},
			want: 2,
		},
		"different websites and ids": {
			in: []rawSponsor{
				{id: "a", website: "https://a.dev"},
				{id: "b", website: "https://b.dev"},
			},
			want: 2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := dedupe(tt.in)
			if len(got) != tt.want {
				t.Fatalf("got %d sponsors, want %d: %+v", len(got), tt.want, got)
			}
			if tt.wantSrc != "" && got[0].source != tt.wantSrc {
				t.Fatalf("got source %q, want %q", got[0].source, tt.wantSrc)
			}
		})
	}
}
