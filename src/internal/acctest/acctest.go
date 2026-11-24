package acctest

import (
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
)

const ProviderName = "omniddi"

func fastClient(cfg *client.Config) {
	cfg.TaskPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, EmptyGrace: 300 * time.Millisecond}
	cfg.ApprovalPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond}
	cfg.RetryBackoff = client.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
}

var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	ProviderName: func() (tfprotov6.ProviderServer, error) {
		return providerserver.NewProtocol6WithError(omniprovider.NewWithClientTuning("test", fastClient)())()
	},
}

func ProviderFactoriesFor(p provider.Provider) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		ProviderName: providerserver.NewProtocol6WithError(p),
	}
}

func ProviderConfig(endpoint string) string {
	return ProviderConfigWith(endpoint, "")
}

func ProviderConfigWith(endpoint, extraAttributes string) string {
	return fmt.Sprintf(`
provider "omniddi" {
  endpoint = %q
  username = %q
  password = %q
  timeout  = "30s"
%s}
`, endpoint, fakeserver.DefaultUsername, fakeserver.DefaultPassword, extraAttributes)
}
