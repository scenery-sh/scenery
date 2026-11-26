package runtime

import (
	"encoding/json"
	"testing"

	"scenery.sh/errs"
)

func TestWorkerBindingFailureKeepsHostCodeAndDelivery(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		reply    string
		code     errs.ErrCode
		delivery any
	}{
		{`{"error":{"kind":"error","message":"service is unavailable","code":"unavailable","meta":{"delivery":"not_sent"}}}`, errs.Unavailable, "not_sent"},
		{`{"error":{"kind":"error","message":"authorization policy denied invocation","code":"permission_denied","meta":null}}`, errs.PermissionDenied, nil},
		{`{"error":{"kind":"error","message":"request does not match the declared input","code":"transport.invalid_request"}}`, errs.InvalidArgument, nil},
		{`{"error":{"kind":"error","message":"contract implementation failure","code":"system.internal"}}`, errs.Internal, nil},
		{`{"error":{"kind":"error","message":"odd","code":"host.future"}}`, errs.Unknown, nil},
	} {
		var response processLinkResponse
		if err := json.Unmarshal([]byte(test.reply), &response); err != nil {
			t.Fatal(err)
		}
		typed, ok := errs.As(response.Error.err())
		if !ok || typed.Code != test.code || typed.Meta["delivery"] != test.delivery || typed.Message == "" {
			t.Errorf("%s decoded as %#v", test.reply, typed)
		}
	}
	plain := (&processLinkError{Kind: "error", Message: "boom"}).err()
	if _, typed := errs.As(plain); typed || plain.Error() != "boom" {
		t.Fatalf("untyped failure = %#v", plain)
	}
}
