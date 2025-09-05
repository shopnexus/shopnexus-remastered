package search

import (
	"context"
	"encoding/json"
	"fmt"
	"shopnexus-remastered/internal/utils/ptr"

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

func (e *ElasticsearchClient) Search(ctx context.Context, params SearchParams) ([]SearchResult, error) {
	resp, err := e.client.Search().
		Index(params.Index).
		Query(&types.Query{
			QueryString: &types.QueryStringQuery{
				Query: params.Query,
			},
		}).
		Size(params.Limit).
		Sort(&types.SortOptions{}).
		SearchAfter(func() []types.FieldValueVariant {
			var sao []types.FieldValueVariant
			for _, v := range params.SearchAfter {
				sao = append(sao, SearchAfterOptions{Value: v})
			}
			return sao
		}()...).
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

func (e *ElasticsearchClient) Close() error {
	// TypedClient doesn't have explicit close method
	// Connection pooling is handled internally
	return nil
}

type SearchAfterOptions struct {
	Value any
}

func (s SearchAfterOptions) FieldValueCaster() *types.FieldValue {
	return ptr.ToPtr(types.FieldValue(s.Value))
}
