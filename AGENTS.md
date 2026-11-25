# ScreenGate

## Windows-klienten

- Når en endring påvirker den installerte Windows-klienten, oppdater `clientVersion` i `cmd/client/protocol.go`. Bruk formatet `ÅÅÅÅ.MM.DD.N`, og øk `N` hvis datoen allerede har en klientversjon.
- Før hver klientendring i `cmd/client/CHANGELOG.md` under den nye versjonen, med nyeste versjon øverst. Beskriv kort hva brukeren eller driften vil merke. Samle versjonsnummeret og endringsloggen med klientendringen i samme leveranse.
