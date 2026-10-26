package model

import "testing"

func TestParseReference(t *testing.T) {
	for _, value := range []string{
		"octo/repo#42", "https://github.com/octo/repo/issues/42",
		"https://github.com/octo/repo/issues/42#issuecomment-1",
		"http://enterprise.example/octo/repo/issues/42?x=1",
	} {
		got, err := ParseReference(value)
		if err != nil || got.Repository != "octo/repo" || got.Number != 42 {
			t.Errorf("ParseReference(%q) = %+v, %v", value, got, err)
		}
	}
	for _, value := range []string{"#42", "octo/repo", "octo/repo#abc", "octo/repo#-1", "https://github.com/octo/repo/pull/42"} {
		if _, err := ParseReference(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestParseThreadReference(t *testing.T) {
	for _, value := range []string{"octo/repo#42", "https://github.com/octo/repo/issues/42#issuecomment-1", "https://GITHUB.COM/octo/repo/issues/42"} {
		ref, err := ParseThreadReference(value)
		if err != nil || ref.Repository != "octo/repo" || ref.Number != 42 {
			t.Errorf("%s: %+v, %v", value, ref, err)
		}
	}
	for _, value := range []string{"https://elsewhere.example/octo/repo/issues/42", "http://github.com/octo/repo/issues/42", "https://github.com:443/octo/repo/issues/42", "https://user@github.com/octo/repo/issues/42", "octo/repo#0", "../repo#1", "octo/..#1", "octo/repo?x#1", "https://github.com/octo/repo/pull/42"} {
		if _, err := ParseThreadReference(value); err == nil {
			t.Errorf("accepted unsafe or unsupported reference %q", value)
		}
	}
}
