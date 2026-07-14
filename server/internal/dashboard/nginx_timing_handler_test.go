package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"controltower/server/internal/storage"
)
type nginxStoreStub struct{b []storage.NginxTimingBucket;s []storage.NginxSlowSample;q storage.NginxTimingQuery}
func(n *nginxStoreStub)QueryNginxTiming(q storage.NginxTimingQuery)([]storage.NginxTimingBucket,error){n.q=q;return n.b,nil}
func(n *nginxStoreStub)QueryNginxSlowSamples(q storage.NginxSlowSampleQuery)([]storage.NginxSlowSample,error){return n.s,nil}
func TestNginxTimingSummaryAndFiltering(t *testing.T){now:=time.Now().UTC();s:=&nginxStoreStub{b:[]storage.NginxTimingBucket{{InstanceID:"i",BucketAt:now,RequestCount:10,Status5xx:2,Status504:1,SlowCount:4,SlowTTFTCount:3,SlowTransferCount:1}}};h:=NewHandler(nil).WithNginxTimingStore(s);rr:=httptest.NewRecorder();h.HandleNginxTiming(rr,httptest.NewRequest(http.MethodGet,"/api/dashboard/nginx-timing?instance_id=i&hours=6",nil));if rr.Code!=200||!strings.Contains(rr.Body.String(),`"slow_ttft_percent":75`){t.Fatalf("status=%d body=%s",rr.Code,rr.Body.String())};if time.Since(s.q.Since)<5*time.Hour{t.Fatalf("since=%v",s.q.Since)}}
func TestNginxTimingValidatesBoundsAndEmpty(t *testing.T){h:=NewHandler(nil).WithNginxTimingStore(&nginxStoreStub{});bad:=httptest.NewRecorder();h.HandleNginxTiming(bad,httptest.NewRequest(http.MethodGet,"/api/dashboard/nginx-timing?instance_id=i&hours=169",nil));if bad.Code!=400{t.Fatalf("bad=%d",bad.Code)};ok:=httptest.NewRecorder();h.HandleNginxSlowSamples(ok,httptest.NewRequest(http.MethodGet,"/api/dashboard/nginx-timing/slow-samples?instance_id=i",nil));if ok.Code!=200||!strings.Contains(ok.Body.String(),`"items":[]`){t.Fatalf("body=%s",ok.Body.String())}}
