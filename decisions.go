package bungkus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

var (
	_ Decider = (*OpenAI)(nil)
	_ Decider = (*SystemOne)(nil)
)

// Question types.
const (
	QuestionYesNo  = "yes_no" // OpenAI "predicate", System One "noul"
	QuestionChoice = "choice"
	QuestionScore  = "score"
)

// AnswerRefusal is the Answer.Type of a question the model declined.
const AnswerRefusal = "refusal"

// Question is one thing a decision model judges about the input.
type Question struct {
	Name         string // required by System One; echoed back in Answer.Name
	Type         string // one of the Question constants
	Instructions string
	// Choices are the options of a choice question, or the levels of a score
	// question from lowest to highest (Value is the level's label).
	Choices []Choice
}

type Choice struct {
	Value       string
	Description string // optional
}

type DecideRequest struct {
	Model     string
	Input     string
	Parts     []Part // images; OpenAI only, as data URLs
	Questions []Question
}

type Answer struct {
	Name string
	Type string // the question's type, or AnswerRefusal

	Probability float64 // yes_no: probability the answer is yes
	Choice      string  // choice: the most likely Value
	Score       float64 // score: expected level index, from 0, may fall between levels
	Confidence  float64 // choice and score, when the model reports it
	// Probabilities maps each choice Value, or score level label, to its
	// probability, when the model reports it.
	Probabilities map[string]float64
}

type DecideResponse struct {
	Answers []Answer // in question order
	Usage   Usage
}

// Answer returns the answer to the question with the given name.
func (r *DecideResponse) Answer(name string) (Answer, bool) {
	for _, a := range r.Answers {
		if a.Name == name {
			return a, true
		}
	}
	return Answer{}, false
}

// Decider asks decision models, which return probabilities, choices and
// scores in one fast pass instead of generating text.
//
// OpenAI implements it with POST /decisions; point it at any compatible
// gateway with WithBaseURL. SystemOne implements the TypeSafe Jev format.
type Decider interface {
	Decide(ctx context.Context, req *DecideRequest) (*DecideResponse, error)
}

func (c *client) decide(ctx context.Context, req *DecideRequest, call func(context.Context) (*DecideResponse, error)) (*DecideResponse, error) {
	ctx, end := StartRun(ctx, RunInfo{Component: ComponentDecider, Name: c.name}, req)
	resp, err := call(ctx)
	end(resp, err)
	return resp, err
}

// Decide calls the OpenAI Decisions API.
func (p *OpenAI) Decide(ctx context.Context, req *DecideRequest) (*DecideResponse, error) {
	return p.decide(ctx, req, func(ctx context.Context) (*DecideResponse, error) {
		body, err := decisionsBody(req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Answers []struct {
				Name          string          `json:"name"`
				Type          string          `json:"type"`
				Probability   float64         `json:"probability"`
				Choice        json.RawMessage `json:"choice"`
				Score         float64         `json:"score"`
				Confidence    float64         `json:"confidence"`
				Probabilities []struct {
					Value       json.RawMessage `json:"value"`
					Label       string          `json:"label"`
					Probability float64         `json:"probability"`
				} `json:"probabilities"`
			} `json:"answers"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := p.post(ctx, "/decisions", body, &out); err != nil {
			return nil, err
		}
		resp := &DecideResponse{Usage: Usage{PromptTokens: out.Usage.InputTokens, CompletionTokens: out.Usage.OutputTokens, TotalTokens: out.Usage.InputTokens + out.Usage.OutputTokens}}
		for _, a := range out.Answers {
			ans := Answer{Name: a.Name, Type: a.Type, Probability: a.Probability, Choice: rawString(a.Choice), Score: a.Score, Confidence: a.Confidence}
			if ans.Type == "predicate" {
				ans.Type = QuestionYesNo
			}
			for _, pr := range a.Probabilities {
				if ans.Probabilities == nil {
					ans.Probabilities = map[string]float64{}
				}
				key := pr.Label // score levels
				if key == "" {
					key = rawString(pr.Value) // choices
				}
				ans.Probabilities[key] = pr.Probability
			}
			resp.Answers = append(resp.Answers, ans)
		}
		return resp, nil
	})
}

func decisionsBody(req *DecideRequest) (map[string]any, error) {
	qs := make([]map[string]any, len(req.Questions))
	for i, q := range req.Questions {
		m := map[string]any{"type": q.Type, "instructions": q.Instructions}
		if q.Name != "" {
			m["name"] = q.Name
		}
		switch q.Type {
		case QuestionYesNo:
			m["type"] = "predicate"
		case QuestionChoice:
			cs := make([]map[string]string, len(q.Choices))
			for j, c := range q.Choices {
				cs[j] = omitEmpty(map[string]string{"value": c.Value, "description": c.Description})
			}
			m["choices"] = cs
		case QuestionScore:
			ls := make([]map[string]string, len(q.Choices))
			for j, c := range q.Choices {
				ls[j] = omitEmpty(map[string]string{"label": c.Value, "description": c.Description})
			}
			m["levels"] = ls
		default:
			return nil, fmt.Errorf("bungkus: unknown question type %q", q.Type)
		}
		qs[i] = m
	}
	var input any = req.Input
	if len(req.Parts) > 0 {
		content := []map[string]string{{"type": "input_text", "text": req.Input}}
		for _, p := range req.Parts {
			if p.Type != PartImage {
				return nil, fmt.Errorf("bungkus: decisions take images only, not %q parts", p.Type)
			}
			content = append(content, map[string]string{"type": "input_image", "image_url": p.URL})
		}
		input = []map[string]any{{"role": "user", "content": content}}
	}
	return map[string]any{"model": req.Model, "input": input, "questions": qs}, nil
}

func omitEmpty(m map[string]string) map[string]string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

// rawString returns a JSON string's value, or other JSON (true, 3) as written.
func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// SystemOne calls a decision model with the System One request format that
// TypeSafe introduced for Jev, also served by Liquid AI, Microsoft Foundry,
// Baseten, Upstage, AIML API and LLM Gateway. It does not take images.
type SystemOne struct{ client }

// NewSystemOne takes the full endpoint URL, which differs by vendor, e.g.
// https://api.aimlapi.com/v1/decisions or https://api.llmgateway.io/v1/systemone.
func NewSystemOne(endpoint, apiKey string, opts ...Option) *SystemOne {
	opts = append([]Option{WithBaseURL(endpoint)}, opts...)
	return &SystemOne{newClient("systemone", "", map[string]string{"Authorization": "Bearer " + apiKey}, opts)}
}

func (p *SystemOne) Decide(ctx context.Context, req *DecideRequest) (*DecideResponse, error) {
	return p.decide(ctx, req, func(ctx context.Context) (*DecideResponse, error) {
		if len(req.Parts) > 0 {
			return nil, fmt.Errorf("bungkus: systemone does not take parts")
		}
		qs := map[string]any{}
		for _, q := range req.Questions {
			if q.Name == "" {
				return nil, fmt.Errorf("bungkus: systemone questions need a Name")
			}
			m := map[string]any{"type": q.Type, "instructions": q.Instructions}
			switch q.Type {
			case QuestionYesNo:
				m["type"] = "noul"
			case QuestionChoice:
				criteria := map[string]string{}
				for _, c := range q.Choices {
					criteria[c.Value] = c.Description
				}
				m["criteria"] = criteria
			case QuestionScore:
				levels := make([]string, len(q.Choices))
				for i, c := range q.Choices {
					levels[i] = c.Value
				}
				m["criteria"] = levels
			default:
				return nil, fmt.Errorf("bungkus: unknown question type %q", q.Type)
			}
			qs[q.Name] = m
		}
		var out struct {
			Answers map[string]struct {
				Type          string             `json:"type"`
				Noul          float64            `json:"noul"`
				Choice        string             `json:"choice"`
				Score         float64            `json:"score"`
				Confidence    float64            `json:"confidence"`
				Probabilities map[string]float64 `json:"probabilities"`
			} `json:"answers"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := p.post(ctx, "", map[string]any{"model": req.Model, "state": req.Input, "questions": qs}, &out); err != nil {
			return nil, err
		}
		resp := &DecideResponse{Usage: Usage{PromptTokens: out.Usage.InputTokens, CompletionTokens: out.Usage.OutputTokens, TotalTokens: out.Usage.InputTokens + out.Usage.OutputTokens}}
		for _, q := range req.Questions { // the answers are a map; keep question order
			a, ok := out.Answers[q.Name]
			if !ok {
				return nil, fmt.Errorf("bungkus: systemone returned no answer for %q", q.Name)
			}
			ans := Answer{Name: q.Name, Type: a.Type, Probability: a.Noul, Choice: a.Choice, Score: a.Score, Confidence: a.Confidence, Probabilities: a.Probabilities}
			if ans.Type == "noul" {
				ans.Type = QuestionYesNo
			}
			if ans.Type == QuestionScore && a.Probabilities != nil { // keyed by level index; use the labels
				ans.Probabilities = map[string]float64{}
				for k, pr := range a.Probabilities {
					if i, err := strconv.Atoi(k); err == nil && i >= 0 && i < len(q.Choices) {
						k = q.Choices[i].Value
					}
					ans.Probabilities[k] = pr
				}
			}
			resp.Answers = append(resp.Answers, ans)
		}
		return resp, nil
	})
}
