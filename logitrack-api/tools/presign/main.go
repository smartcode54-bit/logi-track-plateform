// Command presign prints an AWS Signature V4 presigned GET URL for one object
// (path-style), for the local edge smoke test (developer-spec.md §9.1, §15.2,
// R74; issue TW2): a URL signed for S3_PRESIGN_ENDPOINT must resolve through
// the MEDIA_DOMAIN site. Credentials are read from the env file and never
// printed; only the URL is written to stdout. The api's own presigner arrives
// with T11.
//
// Usage: go run ./tools/presign -env .env -endpoint http://media.localhost -bucket logitrack -key smoke-probe.txt
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

func main() {
	envPath := flag.String("env", ".env", "env file with S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY, S3_REGION")
	endpoint := flag.String("endpoint", "", "origin the URL is signed for, e.g. S3_PRESIGN_ENDPOINT or http://{MEDIA_DOMAIN}")
	bucket := flag.String("bucket", "", "bucket")
	key := flag.String("key", "", "object key")
	expires := flag.Duration("expires", 5*time.Minute, "validity")
	flag.Parse()

	lines, err := dotenv.Read(*envPath)
	if err != nil {
		fail(err)
	}
	env := dotenv.Map(lines)
	for _, n := range []string{"S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY", "S3_REGION"} {
		if env[n] == "" {
			fail(fmt.Errorf("%s is not set in %s", n, *envPath))
		}
	}
	if *endpoint == "" || *bucket == "" || *key == "" {
		fail(fmt.Errorf("-endpoint, -bucket and -key are required"))
	}
	u, err := Presign(*endpoint, *bucket, *key, env["S3_REGION"], env["S3_ACCESS_KEY_ID"], env["S3_SECRET_ACCESS_KEY"], time.Now().UTC(), *expires)
	if err != nil {
		fail(err)
	}
	fmt.Println(u)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "presign:", err)
	os.Exit(2)
}

// Presign returns a SigV4 query-signed GET URL for endpoint/bucket/key
// (bucket empty: virtual-host style, the object path is the key alone).
func Presign(endpoint, bucket, key, region, accessKey, secretKey string, now time.Time, expires time.Duration) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return "", fmt.Errorf("endpoint must be an http(s) origin")
	}
	path := "/" + key
	if bucket != "" {
		path = "/" + bucket + "/" + key
	}
	canonicalPath := encodePath(path)
	amzDate := now.Format("20060102T150405Z")
	scope := now.Format("20060102") + "/" + region + "/s3/aws4_request"

	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprint(int(expires.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	query := canonicalQuery(q)

	canonicalRequest := strings.Join([]string{
		"GET", canonicalPath, query, "host:" + base.Host + "\n", "host", "UNSIGNED-PAYLOAD",
	}, "\n")
	digest := sha256.Sum256([]byte(canonicalRequest))
	toSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(digest[:])}, "\n")

	k := hmacSHA256([]byte("AWS4"+secretKey), now.Format("20060102"))
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))

	return base.Scheme + "://" + base.Host + canonicalPath + "?" + query + "&X-Amz-Signature=" + sig, nil
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// encodePath applies the S3 URI encoding to every path segment ("/" kept).
func encodePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = uriEncode(s)
	}
	return strings.Join(segs, "/")
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, uriEncode(k)+"="+uriEncode(q.Get(k)))
	}
	return strings.Join(parts, "&")
}

// uriEncode is the SigV4 encoding: unreserved characters stay, everything else is %XX.
func uriEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
