package bungkus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// capture returns a server that records the last request's path and JSON
// body, and answers with reply.
func capture(t *testing.T, reply string) (url string, path *string, body *map[string]any) {
	path, body = new(string), new(map[string]any)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.URL.Path
		json.NewDecoder(r.Body).Decode(body)
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, path, body
}

var ticket = &DecideRequest{
	Model: "m",
	Input: "The package arrived broken. I want my money back.",
	Questions: []Question{
		{Name: "damaged", Type: QuestionYesNo, Instructions: "Is an item damaged?"},
		{Name: "queue", Type: QuestionChoice, Instructions: "Which team?", Choices: []Choice{{"billing", "Refunds"}, {Value: "shipping"}}},
		{Name: "urgency", Type: QuestionScore, Instructions: "How urgent?", Choices: []Choice{{Value: "low"}, {Value: "high", Description: "Today"}}},
	},
}

func TestOpenAIDecide(t *testing.T) {
	url, path, body := capture(t, `{"answers":[
		{"type":"predicate","name":"damaged","probability":0.95},
		{"type":"choice","name":"queue","choice":"shipping","confidence":0.81,"probabilities":[{"value":"billing","probability":0.19},{"value":"shipping","probability":0.81}]},
		{"type":"score","name":"urgency","score":0.6,"confidence":0.6,"probabilities":[{"value":0,"label":"low","probability":0.4},{"value":1,"label":"high","probability":0.6}]},
		{"type":"refusal","name":"other"},
		{"type":"choice","name":"flag","choice":true}],
		"usage":{"input_tokens":96,"output_tokens":2}}`)
	resp, err := NewOpenAI("k", WithBaseURL(url)).Decide(context.Background(), ticket)
	if err != nil {
		t.Fatal(err)
	}
	if *path != "/decisions" {
		t.Errorf("path %q", *path)
	}
	want := `{"input":"The package arrived broken. I want my money back.","model":"m","questions":[` +
		`{"instructions":"Is an item damaged?","name":"damaged","type":"predicate"},` +
		`{"choices":[{"description":"Refunds","value":"billing"},{"value":"shipping"}],"instructions":"Which team?","name":"queue","type":"choice"},` +
		`{"instructions":"How urgent?","levels":[{"label":"low"},{"description":"Today","label":"high"}],"name":"urgency","type":"score"}]}`
	if got, _ := json.Marshal(*body); string(got) != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}

	if a, _ := resp.Answer("damaged"); a.Type != QuestionYesNo || a.Probability != 0.95 {
		t.Errorf("damaged: %+v", a)
	}
	if a, _ := resp.Answer("queue"); a.Choice != "shipping" || a.Confidence != 0.81 || a.Probabilities["billing"] != 0.19 {
		t.Errorf("queue: %+v", a)
	}
	if a, _ := resp.Answer("urgency"); a.Score != 0.6 || !reflect.DeepEqual(a.Probabilities, map[string]float64{"low": 0.4, "high": 0.6}) {
		t.Errorf("urgency: %+v", a)
	}
	if a, _ := resp.Answer("other"); a.Type != AnswerRefusal {
		t.Errorf("refusal: %+v", a)
	}
	if a, _ := resp.Answer("flag"); a.Choice != "true" {
		t.Errorf("boolean choice: %+v", a)
	}
	if _, ok := resp.Answer("missing"); ok {
		t.Error("found an answer to an unasked question")
	}
	if resp.Usage != (Usage{96, 2, 98}) {
		t.Errorf("usage %+v", resp.Usage)
	}
}

func TestOpenAIDecideImagesAndErrors(t *testing.T) {
	url, _, body := capture(t, `{"answers":[]}`)
	p := NewOpenAI("k", WithBaseURL(url))
	ctx := context.Background()
	img := Part{Type: PartImage, URL: "data:image/png;base64,AAAA"}
	if _, err := p.Decide(ctx, &DecideRequest{Input: "look", Parts: []Part{img}, Questions: []Question{{Type: QuestionYesNo}}}); err != nil {
		t.Fatal(err)
	}
	if at(*body, "input", 0, "content", 1, "image_url") != img.URL || at(*body, "input", 0, "content", 0, "text") != "look" || at(*body, "questions", 0, "name") != nil {
		t.Errorf("body %v", *body)
	}

	for _, req := range []*DecideRequest{
		{Questions: []Question{{Type: "rank"}}},
		{Parts: []Part{{Type: PartFile}}},
	} {
		if _, err := p.Decide(ctx, req); err == nil {
			t.Errorf("%+v: want an error", req)
		}
	}
	if _, err := NewOpenAI("k", WithBaseURL(serve(t, 400, "bad"))).Decide(ctx, ticket); err == nil {
		t.Error("want an API error")
	}
}

func TestSystemOneDecide(t *testing.T) {
	url, path, body := capture(t, `{"model":"jev-1.14.0","answers":{
		"damaged":{"type":"noul","noul":0.96},
		"queue":{"type":"choice","choice":"billing","confidence":0.98,"probabilities":{"billing":0.99,"shipping":0.01}},
		"urgency":{"type":"score","score":0.7,"confidence":0.69,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.3,"1":0.7,"9":0}}},
		"usage":{"input_tokens":406,"output_tokens":73}}`)
	resp, err := NewSystemOne(url+"/v1/systemone", "k").Decide(context.Background(), ticket)
	if err != nil {
		t.Fatal(err)
	}
	if *path != "/v1/systemone" {
		t.Errorf("path %q", *path)
	}
	want := `{"model":"m","questions":{` +
		`"damaged":{"instructions":"Is an item damaged?","type":"noul"},` +
		`"queue":{"criteria":{"billing":"Refunds","shipping":""},"instructions":"Which team?","type":"choice"},` +
		`"urgency":{"criteria":["low","high"],"instructions":"How urgent?","type":"score"}},` +
		`"state":"The package arrived broken. I want my money back."}`
	if got, _ := json.Marshal(*body); string(got) != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}

	var names []string
	for _, a := range resp.Answers {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "damaged,queue,urgency" {
		t.Errorf("answers not in question order: %v", names)
	}
	if a := resp.Answers[0]; a.Type != QuestionYesNo || a.Probability != 0.96 {
		t.Errorf("damaged: %+v", a)
	}
	if a := resp.Answers[1]; a.Choice != "billing" || a.Probabilities["shipping"] != 0.01 {
		t.Errorf("queue: %+v", a)
	}
	if a := resp.Answers[2]; a.Score != 0.7 || !reflect.DeepEqual(a.Probabilities, map[string]float64{"low": 0.3, "high": 0.7, "9": 0}) {
		t.Errorf("urgency: %+v", a)
	}
	if resp.Usage != (Usage{406, 73, 479}) {
		t.Errorf("usage %+v", resp.Usage)
	}
}

func TestSystemOneErrors(t *testing.T) {
	ctx := context.Background()
	p := NewSystemOne(serve(t, 200, `{"answers":{}}`), "k")
	for _, req := range []*DecideRequest{
		{Parts: []Part{{Type: PartImage}}},
		{Questions: []Question{{Type: QuestionYesNo}}},            // no name
		{Questions: []Question{{Name: "x", Type: "rank"}}},        // unknown type
		{Questions: []Question{{Name: "x", Type: QuestionYesNo}}}, // no answer returned
	} {
		if _, err := p.Decide(ctx, req); err == nil {
			t.Errorf("%+v: want an error", req)
		}
	}
	if _, err := NewSystemOne(serve(t, 500, "down"), "k", WithRetries(0)).Decide(ctx, ticket); err == nil {
		t.Error("want an API error")
	}
}

func TestDecideCallbacks(t *testing.T) {
	var r recorder
	ctx := WithHandlers(context.Background(), &r)
	NewSystemOne(serve(t, 200, `{"answers":{}}`), "k").Decide(ctx, &DecideRequest{})
	if got := strings.Join(r.events, "|"); !strings.Contains(got, "0 start decider/systemone *bungkus.DecideRequest") || !strings.Contains(got, "0 end decider/systemone *bungkus.DecideResponse") {
		t.Errorf("events: %s", got)
	}
}
