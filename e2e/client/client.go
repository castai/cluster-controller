package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/logging"
)

func CreateClient(apiURL, apiToken, userAgent string) (*ClientWithResponses, error) {
	httpClientOption := func(client *Client) error {
		client.Client = &http.Client{
			Transport: logging.NewSubsystemLoggingHTTPTransport("CAST.AI", http.DefaultTransport),
			Timeout:   1 * time.Minute,
		}
		client.RequestEditors = append(client.RequestEditors, func(_ context.Context, req *http.Request) error {
			req.Header.Set("user-agent", userAgent)
			return nil
		})
		return nil
	}

	apiTokenOption := WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
		req.Header.Set("X-API-Key", apiToken)
		return nil
	})

	apiClient, err := NewClientWithResponses(apiURL, httpClientOption, apiTokenOption)
	if err != nil {
		return nil, err
	}

	if resp, err := apiClient.AuthTokenAPIListAuthTokensWithResponse(context.Background(), &AuthTokenAPIListAuthTokensParams{}); err != nil {
		return nil, fmt.Errorf("validating api token (by listing auth tokens): %w", err)
	} else if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("expected status code %d, received %d", http.StatusOK, resp.StatusCode())
	}

	return apiClient, nil
}
