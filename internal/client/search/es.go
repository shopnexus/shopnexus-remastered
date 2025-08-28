package search

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/optype"
)

type ElasticsearchClient struct {
	client *elasticsearch.TypedClient
}

func NewElasticsearchClient(cfg elasticsearch.Config) (*ElasticsearchClient, error) {
	client, err := elasticsearch.NewTypedClient(cfg)
	if err != nil {
		return nil, err
	}

	return &ElasticsearchClient{
		client: client,
	}, nil
}

func (e *ElasticsearchClient) IndexDocuments(ctx context.Context, index string, id string, docs any) error {
	_, err := e.client.Index(index).
		Id(id).
		Document(docs).
		OpType(optype.Create).
		Do(ctx)

	return err
}

func (e *ElasticsearchClient) UpdateDocument(ctx context.Context, index string, id string, doc any) error {
	_, err := e.client.Update(index, id).
		Doc(doc).
		Do(ctx)

	return err
}

func (e *ElasticsearchClient) DeleteDocument(ctx context.Context, index, id string) error {
	_, err := e.client.Delete(index, id).
		Do(ctx)

	return err
}

func (e *ElasticsearchClient) Search(ctx context.Context, index string, query string, limit int) ([]SearchResult, error) {
	resp, err := e.client.Search().
		Index(index).
		Query(&types.Query{
			QueryString: &types.QueryStringQuery{
				Query: query,
			},
		}).
		Size(limit).
		Do(ctx)

	if err != nil {
		return nil, err
	}
	js, _ := json.Marshal(resp.Hits.Hits)
	fmt.Println(string(js))

	var results []SearchResult
	for _, hit := range resp.Hits.Hits {
		results = append(results, SearchResult{
			ID:    *hit.Id_,
			Score: float64(*hit.Score_),
		})
	}

	return results, nil
}

func (e *ElasticsearchClient) Suggest(ctx context.Context, index string, query string) ([]string, error) {
	resp, err := e.client.Search().
		Index(index).
		Suggest(&types.Suggester{
			Suggesters: map[string]types.FieldSuggester{
				"suggestions": {
					Text: &query,
					Term: &types.TermSuggester{
						Field: "title", // Adjust field as needed
					},
				},
			},
		}).
		Do(ctx)

	fmt.Println("Suggest response:", resp)

	if err != nil {
		return nil, err
	}

	var suggestions []string
	//if resp.Suggest != nil {
	//	if termSuggestions, ok := resp.Suggest["suggestions"]; ok {
	//		for _, suggestion := range termSuggestions {
	//			if suggestion.Term != nil {
	//				for _, option := range suggestion.Term.Options {
	//					suggestions = append(suggestions, option.Text)
	//				}
	//			}
	//		}
	//	}
	//}

	return suggestions, nil
}

func (e *ElasticsearchClient) Close() error {
	// TypedClient doesn't have explicit close method
	// Connection pooling is handled internally
	return nil
}
