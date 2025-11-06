// Package ros_addrlist provides a MosDNS plugin for automatically adding IP addresses
// from DNS query results to RouterOS address lists via its REST API.
package ros_addrlist

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"

	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/netip"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/IrineSistiana/mosdns/v5/pkg/query_context"
	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// PluginType is the unique identifier for the ros_addrlist plugin
const PluginType = "ros_addrlist"

func init() {
	coremain.RegNewPluginFunc(PluginType, Init, func() any { return new(Args) })
}

// Args defines the configuration parameters for the ros_addrlist plugin.
//
// Example configuration:
//
//	plugins:
//	  - tag: ros_addrlist_exec
//	    type: ros_addrlist
//	    args:
//	      addrlist: "mosdns_gfwlist"
//	      server: "http://192.168.88.1:80"
//	      user: "yourusername"
//	      passwd: "yourpasswd"
//	      mask4: 24
//	      mask6: 32
type Args struct {
	// AddrList is the name of the RouterOS address list to add IPs to
	AddrList string `yaml:"addrlist"`

	// Server is the RouterOS REST API endpoint URL
	Server string `yaml:"server"`

	// User is the RouterOS API username
	User string `yaml:"user"`

	// Passwd is the RouterOS API password
	Passwd string `yaml:"passwd"`

	// Mask4 is the subnet mask for IPv4 addresses (default: 24)
	Mask4 int `yaml:"mask4"`

	// Mask6 is the subnet mask for IPv6 addresses (default: 32)
	Mask6 int `yaml:"mask6"`
}

// rosAddrlistPlugin implements the RouterOS address list plugin functionality
type rosAddrlistPlugin struct {
	args   *Args        // Plugin configuration
	client *http.Client // HTTP client for RouterOS API calls
	logger *zap.Logger  // Logger instance
}

// Init initializes the plugin with the given configuration
// Implements the plugin.Executable interface
func Init(bp *coremain.BP, args any) (any, error) {
	return newRosAddrlistPlugin(args.(*Args), bp.L())
}

// newRosAddrlistPlugin creates a new instance of rosAddrlistPlugin with the provided configuration
// It configures the HTTP client with TLS settings and timeouts
func newRosAddrlistPlugin(args *Args, logger *zap.Logger) (*rosAddrlistPlugin, error) {
	if args.Mask4 == 0 {
		args.Mask4 = 24
	}
	if args.Mask6 == 0 {
		args.Mask6 = 32
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		IdleConnTimeout: 30 * time.Second,
		MaxIdleConns:    10,
	}
	client := &http.Client{
		Timeout:   time.Second * 2,
		Transport: tr,
	}

	return &rosAddrlistPlugin{
		args:   args,
		client: client,
		logger: logger,
	}, nil
}

// Exec implements the plugin.Executable interface
// It processes DNS query responses and adds any IP addresses found to the RouterOS address list
func (p *rosAddrlistPlugin) Exec(ctx context.Context, qCtx *query_context.Context) error {
	r := qCtx.R()
	if r != nil {
		if err := p.addIP(r); err != nil {
			p.logger.Error("failed to add IP to RouterOS", zap.Error(err))
			return fmt.Errorf("addip failed but ignored: %w", err)
		}
	}
	return nil
}

// addIPViaHTTPRequest sends an HTTP request to RouterOS API to add an IP address to the specified address list
// It handles both IPv4 and IPv6 addresses and includes the source domain as a comment
func (p *rosAddrlistPlugin) addIPViaHTTPRequest(ip *net.IP, v6 bool, domain string) error {
	// request to add ips via http request routeros RESTFul API
	t := "ip"
	if v6 {
		t = "ipv6"
	}
	routerURL := p.args.Server + "/rest/" + t + "/firewall/address-list/add"
	payload := map[string]interface{}{
		"address": ip.String(),
		"list":    p.args.AddrList,
		"comment": "[mosdns] domain: " + domain,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal json data: %w", err)
	}

	req, err := http.NewRequest("POST", routerURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(p.args.User, p.args.Passwd)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute http request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		p.logger.Info("added ip to ros addrlist", zap.String("ip", ip.String()), zap.String("domain", domain))
		// success
		return nil
	case http.StatusBadRequest:
		p.logger.Debug("likely ip already exists", zap.String("ip", ip.String()), zap.String("domain", domain))
		// likely the ip already exists in the addrlist, ignore
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("unauthorized code: %d - %s - %s", resp.StatusCode, ip, domain)
	case http.StatusInternalServerError:
		return fmt.Errorf("internal server error code: %d - %s - %s", resp.StatusCode, ip, domain)
	default:
		return fmt.Errorf("unexpected status code: %d - %s - %s", resp.StatusCode, ip, domain)
	}

}

// addIP processes DNS response message and extracts IP addresses from A and AAAA records
// It validates the IP addresses and adds them to the RouterOS address list
func (p *rosAddrlistPlugin) addIP(r *dns.Msg) error {
	// Skip processing if no address list is configured
	if len(p.args.AddrList) == 0 {
		p.logger.Debug("no address list configured, skipping DNS response processing")
		return nil
	}

	for i := range r.Answer {
		switch rr := r.Answer[i].(type) {
		case *dns.A:
			// Validate IPv4 address
			_, ok := netip.AddrFromSlice(rr.A.To4())
			if !ok {
				return fmt.Errorf("invalid A record with ip: %s", rr.A)
			}
			if err := p.addIPViaHTTPRequest(&rr.A, false, r.Question[0].Name); err != nil {
				return fmt.Errorf("failed to add IPv4 address: %s: %v", rr.A, err)
			}
			p.logger.Debug("successfully added IPv4 address",
				zap.String("ip", rr.A.String()),
				zap.String("domain", r.Question[0].Name))

		case *dns.AAAA:
			// Validate IPv6 address
			_, ok := netip.AddrFromSlice(rr.AAAA.To16())
			if !ok {
				return fmt.Errorf("invalid AAAA record with ip: %s", rr.AAAA)
			}
			if err := p.addIPViaHTTPRequest(&rr.AAAA, true, r.Question[0].Name); err != nil {
				return fmt.Errorf("failed to add IPv6 address: %s, %v", rr.AAAA, err)
			}
			p.logger.Debug("successfully added IPv6 address",
				zap.String("ip", rr.AAAA.String()),
				zap.String("domain", r.Question[0].Name))
		}
	}

	return nil
}

// Close implements io.Closer interface
// Performs cleanup when the plugin is being shut down
func (p *rosAddrlistPlugin) Close() error {
	p.logger.Info("closing ros_addrlist plugin")
	return nil
}
