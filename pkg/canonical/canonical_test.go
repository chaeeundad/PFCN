package canonical

import (
	"testing"
)

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{`{ "a" : [ 1 , true , null , "x" ] }`, `{"a":[1,true,null,"x"]}`},
		{`{"s":"<>& /é"}`, `{"s":"<>&` + " " + `/é"}`},
		{`{"c":"\u0001\n"}`, `{"c":"\u0001\n"}`},
		// UTF-16 ordering: U+FB33 (one unit) sorts after U+1D11E (surrogate pair 0xD834...).
		{`{"דּ":1,"𝄞":2}`, `{"` + "\U0001D11E" + `":2,"` + "דּ" + `":1}`},
	}
	for _, c := range cases {
		got, err := Canonicalize([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if string(got) != c.want {
			t.Errorf("%s: got %s want %s", c.in, got, c.want)
		}
	}
}

func TestRejectsFloatsAndUnsafeIntegers(t *testing.T) {
	for _, in := range []string{`{"a":1.5}`, `{"a":1e3}`, `{"a":9007199254740992}`} {
		if _, err := Canonicalize([]byte(in)); err == nil {
			t.Errorf("%s: expected error", in)
		}
	}
}

func TestCheck(t *testing.T) {
	if err := Check([]byte(`{"a":1,"b":2}`)); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{`{"b":2,"a":1}`, `{"a":1, "b":2}`, `{"a":1,"a":2}`} {
		if err := Check([]byte(in)); err == nil {
			t.Errorf("%s: expected non-canonical", in)
		}
	}
}

func TestUnmarshalRejectsUnknownFields(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}
	if err := Unmarshal([]byte(`{"a":1,"b":2}`), &v); err == nil {
		t.Fatal("expected unknown field error")
	}
	if err := Unmarshal([]byte(`{"a":1}`), &v); err != nil || v.A != 1 {
		t.Fatalf("got %v %v", v, err)
	}
}
