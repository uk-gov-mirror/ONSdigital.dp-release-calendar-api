package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ONSdigital/dp-api-clients-go/v2/headers"
	"github.com/ONSdigital/dp-api-clients-go/v2/health"
	"github.com/ONSdigital/dp-api-clients-go/v2/zebedee"
	dprequest "github.com/ONSdigital/dp-net/v3/request"
	"github.com/ONSdigital/dp-release-calendar-api/mapper"
	"github.com/gorilla/mux"
	. "github.com/smartystreets/goconvey/convey"
)

func TestLegacyHandler(t *testing.T) {
	Convey("Given a Legacy handler ", t, func() {
		url := "/release/adoption"
		r := zebedee.Release{URI: url, Description: zebedee.Description{Title: "Test"}}
		mockZebedeeClient := &ZebedeeClientMock{
			GetReleaseFunc: func(ctx context.Context, userAccessToken, collectionId, lang, uri string) (zebedee.Release, error) {
				return r, nil
			},
		}
		handler := LegacyHandler(context.Background(), mockZebedeeClient)

		Convey("when a valid request without headers is received (web)", func() {
			req := httptest.NewRequest("GET", fmt.Sprintf("http://localhost:8080/v1/releases/legacy?url=%s", url), http.NoBody)
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			Convey("Then the call to Zebedee is correct", func() {
				So(len(mockZebedeeClient.GetReleaseCalls()), ShouldEqual, 1)
				So(mockZebedeeClient.GetReleaseCalls()[0].UserAccessToken, ShouldEqual, "")
				So(mockZebedeeClient.GetReleaseCalls()[0].CollectionID, ShouldEqual, "")
				So(mockZebedeeClient.GetReleaseCalls()[0].Lang, ShouldEqual, "en")
				So(mockZebedeeClient.GetReleaseCalls()[0].URI, ShouldEqual, url)
			})
			Convey("And the response is correct", func() {
				So(resp.Code, ShouldEqual, http.StatusOK)
				expectedJSON, _ := json.Marshal(mapper.CreateReleaseFromZebedee(r))
				So(resp.Body.Bytes(), ShouldResemble, expectedJSON)
				So(len(mockZebedeeClient.GetReleaseCalls()), ShouldEqual, 1)
			})
		})

		Convey("when a valid request with headers is received (publishing)", func() {
			accessToken := "user-access-token"
			collectionID := "my-collection"

			req := httptest.NewRequest("GET", fmt.Sprintf("http://localhost:8080/v1/releases/legacy?url=%s&lang=cy", url), http.NoBody)
			headers.SetAuthToken(req, accessToken)
			headers.SetCollectionID(req, collectionID)

			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			Convey("Then the call to Zebedee is correct", func() {
				So(len(mockZebedeeClient.GetReleaseCalls()), ShouldEqual, 1)
				So(mockZebedeeClient.GetReleaseCalls()[0].UserAccessToken, ShouldEqual, accessToken)
				So(mockZebedeeClient.GetReleaseCalls()[0].CollectionID, ShouldEqual, collectionID)
				So(mockZebedeeClient.GetReleaseCalls()[0].Lang, ShouldEqual, "cy")
				So(mockZebedeeClient.GetReleaseCalls()[0].URI, ShouldEqual, url)
			})
			Convey("And the response is correct", func() {
				So(resp.Code, ShouldEqual, http.StatusOK)
				expectedJSON, _ := json.Marshal(mapper.CreateReleaseFromZebedee(r))
				So(resp.Body.Bytes(), ShouldResemble, expectedJSON)
				So(len(mockZebedeeClient.GetReleaseCalls()), ShouldEqual, 1)
			})
		})

		Convey("when a request without an url parameter is received", func() {
			req := httptest.NewRequest("GET", "http://localhost:8080/v1/releases/legacy", http.NoBody)
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			Convey("Then an error is returned", func() {
				So(resp.Code, ShouldEqual, http.StatusNotFound)
				So(resp.Body.String(), ShouldResemble, "URL not found\n")
			})
		})

		Convey("when a request with an empty url parameter is received", func() {
			req := httptest.NewRequest("GET", "http://localhost:8080/v1/releases/legacy?url=", http.NoBody)
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			Convey("Then an error is returned", func() {
				So(resp.Code, ShouldEqual, http.StatusNotFound)
				So(resp.Body.String(), ShouldResemble, "URL not found\n")
			})
		})
	})
}

func TestLegacyHandlerForwardsToZebedee(t *testing.T) {
	type backendRequest struct {
		method   string
		path     string
		rawQuery string
		header   http.Header
		body     string
	}

	received := make(chan backendRequest, 16)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		received <- backendRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(body)}
		w.Header().Set("Content-Type", "application/json")
		release := `{"uri":"/release/adoption"}`
		switch {
		case r.URL.Query().Has("description"):
			release = `{"description":{"title":"Document"}}`
		case r.URL.Path == "/v1/data/test-collection":
			release = `{"uri":"/release/adoption","relatedDocuments":[{"uri":"/doc/1"}]}`
		}
		_, _ = w.Write([]byte(release))
	}))
	defer backend.Close()

	client := zebedee.NewWithHealthClient(health.NewClient("zebedee", backend.URL+"/v1"))
	router := mux.NewRouter()
	Setup(context.Background(), router, client)
	handler := dprequest.HandlerRequestID(16)(router)

	request := httptest.NewRequest(http.MethodGet, "/v1/releases/legacy?url=%2Frelease%2Fadoption&url=%2Fignored&lang=cy&lang=en&ignored=1", strings.NewReader(`{"ignored":true}`))
	headers.SetAuthToken(request, "test-token")
	headers.SetCollectionID(request, "test-collection")
	const requestID = "caller-request-id"
	request.Header.Set(dprequest.RequestHeaderKey, requestID)
	request.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Client-Only", "not-forwarded")
	request.AddCookie(&http.Cookie{Name: "session", Value: "not-forwarded"})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}

	backendCall := <-received
	if backendCall.method != http.MethodGet || backendCall.path != "/v1/data/test-collection" {
		t.Errorf("backend method and path: got %s %s, want GET /v1/data/test-collection", backendCall.method, backendCall.path)
	}
	if backendCall.rawQuery != "uri=%2Frelease%2Fadoption&lang=cy" {
		t.Errorf("backend query: got %q, want %q", backendCall.rawQuery, "uri=%2Frelease%2Fadoption&lang=cy")
	}
	for _, name := range []string{"Authorization", "X-Florence-Token"} {
		if got := backendCall.header.Get(name); got != "Bearer test-token" {
			t.Errorf("backend %s: got %q, want %q", name, got, "Bearer test-token")
		}
	}
	parts := strings.Split(backendCall.header.Get(dprequest.RequestHeaderKey), ",")
	if len(parts) != 2 || parts[0] != requestID || len(parts[1]) != len(requestID)/2 {
		t.Errorf("backend request ID: got %q, want %q followed by a generated hop ID", backendCall.header.Get(dprequest.RequestHeaderKey), requestID)
	}
	for _, name := range []string{"traceparent", "Content-Type", "Cookie", "X-Client-Only", dprequest.CollectionIDHeaderKey} {
		if got := backendCall.header.Get(name); got != "" {
			t.Errorf("backend %s: got %q, want no copied header", name, got)
		}
	}
	if backendCall.body != "" {
		t.Errorf("backend body: got %q, want empty", backendCall.body)
	}
	if len(received) != 1 {
		t.Fatalf("related-description lookups: got %d, want 1", len(received))
	}
	descriptionCall := <-received
	if descriptionCall.method != http.MethodGet || descriptionCall.path != "/v1/data/test-collection" || descriptionCall.rawQuery != "uri=%2Fdoc%2F1&description&lang=cy" {
		t.Errorf("description lookup: got %s %s?%s, want GET /v1/data/test-collection?uri=%%2Fdoc%%2F1&description&lang=cy", descriptionCall.method, descriptionCall.path, descriptionCall.rawQuery)
	}
	for _, name := range []string{"Authorization", "X-Florence-Token"} {
		if got := descriptionCall.header.Get(name); got != "Bearer test-token" {
			t.Errorf("description lookup %s: got %q, want %q", name, got, "Bearer test-token")
		}
	}
	parts = strings.Split(descriptionCall.header.Get(dprequest.RequestHeaderKey), ",")
	if len(parts) != 2 || parts[0] != requestID || len(parts[1]) != len(requestID)/2 {
		t.Errorf("description lookup request ID: got %q, want %q followed by a generated hop ID", descriptionCall.header.Get(dprequest.RequestHeaderKey), requestID)
	}
	if got := descriptionCall.header.Get("traceparent"); got != "" {
		t.Errorf("description lookup traceparent: got %q, want no copied header", got)
	}

	t.Run("generates an ID and uses the Florence cookie", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/v1/releases/legacy?url=%2Frelease%2Fadoption", http.NoBody)
		request.AddCookie(&http.Cookie{Name: dprequest.FlorenceCookieKey, Value: "cookie-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
		}

		backendCall := <-received
		if backendCall.method != http.MethodGet || backendCall.path != "/v1/data" || backendCall.rawQuery != "uri=%2Frelease%2Fadoption&lang=en" {
			t.Errorf("backend request: got %s %s?%s, want GET /v1/data?uri=%%2Frelease%%2Fadoption&lang=en", backendCall.method, backendCall.path, backendCall.rawQuery)
		}
		for _, name := range []string{"Authorization", "X-Florence-Token"} {
			if got := backendCall.header.Get(name); got != "Bearer cookie-token" {
				t.Errorf("backend %s: got %q, want %q", name, got, "Bearer cookie-token")
			}
		}
		generatedID := request.Header.Get(dprequest.RequestHeaderKey)
		parts := strings.Split(backendCall.header.Get(dprequest.RequestHeaderKey), ",")
		if len(generatedID) != 16 || len(parts) != 2 || parts[0] != generatedID || len(parts[1]) != len(generatedID)/2 {
			t.Errorf("backend request ID: got %q, want generated inbound ID %q followed by a hop ID", backendCall.header.Get(dprequest.RequestHeaderKey), generatedID)
		}
		if got := backendCall.header.Get("Cookie"); got != "" {
			t.Errorf("backend Cookie: got %q, want no forwarded cookie", got)
		}
		if got := len(received); got != 0 {
			t.Errorf("additional web lookups: got %d, want 0", got)
		}
	})
}
