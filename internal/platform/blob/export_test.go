package blob

import "net/url"

// NewS3ForTest builds an S3 store on a local test server (plain http on 127.0.0.1).
func NewS3ForTest(endpoint, bucket, prefix string, creds S3Credentials) (*S3, error) {
	u := &url.URL{Scheme: "s3", Host: bucket, Path: "/" + prefix, RawQuery: url.Values{"endpoint": {"http://s3"}}.Encode()}
	s, err := newS3(u, creds)
	if err != nil {
		return nil, err
	}
	ep, _ := url.Parse(endpoint)
	s.client, err = newMinio(ep.Host, false, "us-east-1", creds)
	return s, err
}
