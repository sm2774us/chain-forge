## What / why
## Checklist
- [ ] Conventional-commit title (`feat:`, `fix:`, `feat!:` for breaking)
- [ ] `npx nx affected -t lint test coverage build` passes (100% coverage gates)
- [ ] Touches signing/custody? Reviewed by @security; no key material in logs/tests
- [ ] Contract change? Updated `contracts/` + all three languages
