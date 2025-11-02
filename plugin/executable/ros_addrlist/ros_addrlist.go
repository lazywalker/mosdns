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

const PluginType = "ros_addrlist"

func init() {
	coremain.RegNewPluginFunc(PluginType, Init, func() any { return new(Args) })
}

// # sample config
// - tag: ros_addrlist_exec
//   - tag: ros_addrlist_exec
//     type: ros_addrlist
//     args:
//     addrlist: "mosdns_gfwlist"
//     server: "http://192.168.88.1:80"
//     user: "yourusername"
//     passwd: "yourpasswd"
//     mask4: 24
//     mask6: 32
type Args struct {
	AddrList string `yaml:"addrlist"`
	Server   string `yaml:"server"`
	User     string `yaml:"user"`
	Passwd   string `yaml:"passwd"`
	Mask4    int    `yaml:"mask4"` // default 24
	Mask6    int    `yaml:"mask6"` // default 32
}

type rosAddrlistPlugin struct {
	args   *Args
	client *http.Client
	logger *zap.Logger
}

func Init(bp *coremain.BP, args any) (any, error) {
	return newRosAddrlistPlugin(args.(*Args), bp.L())
}

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

func (p *rosAddrlistPlugin) Exec(ctx context.Context, qCtx *query_context.Context) error {
	r := qCtx.R()
	if r != nil {
		if err := p.addIP(r); err != nil {
			return fmt.Errorf("addip failed but ignored: %w", err)
		}
	}
	return nil
}

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

func (p *rosAddrlistPlugin) addIP(r *dns.Msg) error {
	for i := range r.Answer {
		switch rr := r.Answer[i].(type) {
		case *dns.A:
			if len(p.args.AddrList) == 0 {
				continue
			}
			_, ok := netip.AddrFromSlice(rr.A.To4())
			if !ok {
				return fmt.Errorf("invalid A record with ip: %s", rr.A)
			}
			if err := p.addIPViaHTTPRequest(&rr.A, false, r.Question[0].Name); err != nil {
				return fmt.Errorf("failed to add ip: %s: %v", rr.A, err)
			}

		case *dns.AAAA:
			if len(p.args.AddrList) == 0 {
				continue
			}
			_, ok := netip.AddrFromSlice(rr.AAAA.To16())
			if !ok {
				return fmt.Errorf("invalid AAAA record with ip: %s", rr.AAAA)
			}
			if err := p.addIPViaHTTPRequest(&rr.AAAA, true, r.Question[0].Name); err != nil {
				return fmt.Errorf("failed to add ip: %s, %v", rr.AAAA, err)
			}
		default:
			continue
		}
	}

	return nil
}

func (p *rosAddrlistPlugin) Close() error {
	return nil
}
