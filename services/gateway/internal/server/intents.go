package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/chainforge/gateway/internal/grpcx"
	"github.com/chainforge/gateway/internal/intent"
	"github.com/chainforge/gateway/internal/relay"
	"github.com/chainforge/gateway/internal/rpcx"
	"github.com/chainforge/gateway/internal/signerclient"
)

func msg(m string) map[string]string { return map[string]string{"error": m} }

func signerFail(w http.ResponseWriter, err error) {
	var ae *signerclient.APIError
	if errors.As(err, &ae) {
		writeJSON(w, ae.Status, msg(ae.Message))
		return
	}
	writeJSON(w, http.StatusBadGateway, msg("signer unavailable"))
}

// fail maps domain and infrastructure errors onto honest HTTP statuses.
func fail(w http.ResponseWriter, err error) {
	var (
		ie *intent.InvalidError
		gs *grpcx.Status
		re *rpcx.Error
	)
	switch {
	case errors.Is(err, relay.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, msg("malformed raw transaction"))
	case errors.As(err, &ie):
		writeJSON(w, http.StatusBadRequest, msg(ie.Msg))
	case errors.As(err, &gs):
		switch gs.Code {
		case grpcx.InvalidArgument:
			writeJSON(w, http.StatusBadRequest, msg(gs.Message))
		case grpcx.ResourceExhausted, grpcx.FailedPrecondition:
			writeJSON(w, http.StatusUnprocessableEntity, msg(gs.Message))
		case grpcx.Unavailable, grpcx.DeadlineExceeded:
			writeJSON(w, http.StatusServiceUnavailable, msg("simulation engine unavailable"))
		default:
			writeJSON(w, http.StatusBadGateway, msg("simulation engine error"))
		}
	case errors.As(err, &re):
		writeJSON(w, http.StatusBadGateway, msg("node error: "+re.Message))
	default:
		writeJSON(w, http.StatusBadGateway, msg("upstream unavailable"))
	}
}

type intentBody struct {
	Chain string `json:"chain"`
	intent.EVMRequest
	Tx   string `json:"tx"` // solana: base64 transaction
	Sign *struct {
		KeyID string `json:"key_id"`
	} `json:"sign"`
}

type intentResp struct {
	Chain          string                   `json:"chain"`
	Tx             *intent.Tx               `json:"tx,omitempty"`
	Simulation     any                      `json:"simulation"`
	Signed         *signerclient.TxResponse `json:"signed,omitempty"`
	SigningSkipped string                   `json:"signing_skipped,omitempty"`
}

func (s *server) simulate(w http.ResponseWriter, r *http.Request, c string) { s.doIntent(w, r, false) }
func (s *server) intentRoute(w http.ResponseWriter, r *http.Request, c string) {
	s.doIntent(w, r, true)
}

func (s *server) doIntent(w http.ResponseWriter, r *http.Request, allowSign bool) {
	if s.Intents == nil {
		writeJSON(w, http.StatusNotImplemented, msg("simulation engine not configured"))
		return
	}
	var b intentBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		writeJSON(w, http.StatusBadRequest, msg("body must be a JSON intent"))
		return
	}
	switch b.Chain {
	case "solana":
		if b.Sign != nil {
			writeJSON(w, http.StatusBadRequest, msg("custodial signing is EVM-only; sign Solana transactions in the wallet"))
			return
		}
		sim, err := s.Intents.SimulateSolana(r.Context(), b.Tx)
		if err != nil {
			fail(w, err)
			return
		}
		s.Metrics.Inc("gateway_simulations_total", map[string]string{"chain": "solana", "outcome": map[bool]string{true: "success", false: "error"}[sim.Success]})
		writeJSON(w, 200, intentResp{Chain: "solana", Simulation: sim})
	case "", "evm":
		p, err := s.Intents.PrepareEVM(r.Context(), b.EVMRequest)
		if err != nil {
			fail(w, err)
			return
		}
		s.Metrics.Inc("gateway_simulations_total", map[string]string{"chain": "evm", "outcome": p.Simulation.Outcome})
		s.Metrics.Set("gateway_engine_elapsed_us", nil, float64(p.Simulation.ElapsedUS))
		out := intentResp{Chain: "evm", Tx: &p.Tx, Simulation: p.Simulation}
		if allowSign && b.Sign != nil {
			switch {
			case p.Simulation.Outcome != "success":
				out.SigningSkipped = "simulation " + p.Simulation.Outcome + "; nothing was signed"
			case p.Tx.To == "":
				out.SigningSkipped = "contract creation is not signable by policy"
			default:
				t, err := s.Signer.SignTx(r.Context(), signerclient.TxRequest{
					KeyID: b.Sign.KeyID, ChainID: p.Tx.ChainID, Nonce: p.Tx.Nonce, GasPrice: p.Tx.GasPrice,
					GasLimit: p.Tx.GasLimit, To: p.Tx.To, Value: p.Tx.Value, Data: p.Tx.Data,
				})
				if err != nil {
					signerFail(w, err)
					return
				}
				out.Signed = &t
			}
		}
		writeJSON(w, 200, out)
	default:
		writeJSON(w, http.StatusBadRequest, msg(`chain must be "evm" or "solana"`))
	}
}

func (s *server) broadcast(w http.ResponseWriter, r *http.Request, _ string) {
	var b struct {
		Chain string `json:"chain"`
		RawTx string `json:"raw_tx"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || b.RawTx == "" {
		writeJSON(w, http.StatusBadRequest, msg("body must be {chain, raw_tx}"))
		return
	}
	if b.Chain == "" {
		b.Chain = "evm"
	}
	rl, ok := s.Relays[b.Chain]
	if !ok {
		writeJSON(w, http.StatusNotImplemented, msg("no private relay configured for "+b.Chain))
		return
	}
	id, err := rl.Send(r.Context(), b.RawTx)
	if err != nil {
		s.Metrics.Inc("gateway_broadcasts_total", map[string]string{"chain": b.Chain, "outcome": "error"})
		fail(w, err)
		return
	}
	s.Metrics.Inc("gateway_broadcasts_total", map[string]string{"chain": b.Chain, "outcome": "sent"})
	writeJSON(w, 200, map[string]string{"chain": b.Chain, "tx_id": id})
}
