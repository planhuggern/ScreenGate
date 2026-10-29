# Steg 5: fjern rester

Oppryddingen fjerner kode uten aktive kallere og lar testene følge reglene som produktet faktisk bruker. Tilgangsregler, rapportformat, signerte klientfiler og databaseskjema er videreført. Klientversjonen er fortsatt **2026.10.28.4**, siden denne delen ikke endrer klientoppførsel.

## Det som er fjernet

| Kode | Hvorfor den kunne fjernes | Hva som brukes videre |
| --- | --- | --- |
| `cmd/client/blocked.go` og `blocked_test.go` | Tilstandsmodellen hadde ingen kallere utenfor sine egne tester. | Klientløkken, `clientState` og testene av faktiske feiloverganger. |
| `screenTimeDecision` og dens separate test | Funksjonen hadde ingen rolle i serverens beslutninger. | `evaluatePolicy`. De fire kvoteeksemplene er dekket i `TestPolicyDecisions`, inkludert nøyaktig oppbrukt kvote og overforbruk. |
| `userPolicy.day` | Ingen kallere etter at kalenderreglene ble samlet. | `screenpolicy.Rules.Day` gjennom de aktive kalenderreglene. |
| `calculateDailyTotal` | En ekstra inngang som bare testene brukte. | `calculateDailyTotalInLocation`, som også serveren bruker. Testene oppgir tidssonen direkte. |
| `repository.userQuota` og `userPolicyVersion` | Separate lesefunksjoner som bare testene brukte. | Den aktive `service.userPolicy`, som leser kvote og versjon sammen med de øvrige reglene. |
| `service.addHeartbeat` og `repository.addHeartbeat` | En alternativ inngang som bare testene brukte til å legge inn historikk. | Testhjelpen `addHistoricalHeartbeat` i `historical_heartbeat_test.go`. Den inngår ikke i programbygget. |

Historikkhjelpen bruker fortsatt den ordinære databaselagringen. Den kan oppgi gamle tidspunkter for testdata. En virkelig heartbeat går gjennom `recordHeartbeat`, som validerer forespørselen og bruker serverens tid. Navnet og plasseringen gjør forskjellen synlig.

## Vurdering av eldre felt og data

At alle synlige PC-er har en nyere klientversjon beviser ikke at gamle tilstandsfiler, sikkerhetskopier eller rapporter aldri vil dukke opp igjen. Det er ikke avtalt noen minste støttede versjon eller sletting av historikk. Derfor beholdes følgende:

| Felt eller data | Hvorfor det beholdes |
| --- | --- |
| Heartbeat-feltene `active_seconds`, `activity_date`, `reported_at` og `session_state` på serveren | Eldre klienter kan sende dem. Serverens JSON-kontroll kjenner feltene, og overvåkingen skiller manglende aktivitetssekunder fra null. Det betyr ikke at klientens gamle tall styrer dagens forbruksberegning. |
| `locked`, `usage` og `session_diagnostics` | Dette er aktive rapportfelt. `locked` viderefører også dagens reserveverdi ved Windows-feil; diagnostikken forklarer usikkerheten separat. |
| `PendingSeconds`, `PendingDate` og de gamle aktivitetsfeltene i klientens lagrede rapport | De inngår i lesing og videreføring av eldre klienttilstand og den eldre svarbehandlingen. Nye nettverksrapporter bruker `usage`, men denne delen endrer ikke filformatet eller fjerner kompatibilitetsbehandlingen. |
| `LeaseExpiresAt` og svarfeltene for lease/overgang | Eldre filer og klienter kjenner feltene. `LeaseExpiresAt` brukes også ved gjenkjenning av gammel starttilstand. Den korte leasen styrer fortsatt ikke ny klients offline-tilgang. |
| Overgangsrapporten `usage.legacy` | En eldre lagret restkvote kan fortsatt måtte avstemmes første gang en oppgradert klient får kontakt. Den må ikke bli fri tid eller belastes to ganger. |
| Gamle heartbeat-krediteringer og tilstedeværelsesfelt i databasen | Historikk, eksport og eldre klienters beregning bruker dem. Nye målte intervaller og gamle krediteringer inngår i samme regnskap uten dobbeltelling. |
| Eksisterende databasemigreringer | En eldre database eller sikkerhetskopi må fortsatt kunne oppgraderes gjennom hele migreringsrekken. |
| Valgfri programrapportering | Funksjonen er uttrykkelig beholdt for senere bruk. Den er avslått som standard og påvirker ikke kvoten. |

En senere fjerning av kompatibilitetsfelter krever at vi bestemmer hvilke gamle klienter og filer som fortsatt skal støttes, og hvordan dataene skal overføres. Denne oppryddingen trenger ingen ny migrering og sletter ingen lagrede data.

## Kontroll og neste steg

Eksisterende tester kontrollerer fortsatt klientløkken, signerte gamle filer, omstart uten påfyll, avstemming, eldre rapporter, databaseoppgradering, historikk og Windows-diagnostikk. Tester av ubrukt tilstandskode er fjernet; kvoteeksemplene er flyttet til den aktive regelvurderingen.

Kontrollene er `go test ./...`, `go test -race ./...`, `go vet ./...` og egne server-/Windows-klientbygg under `.artifacts`. Ingen programprosess er startet, installert eller rullet ut som del av arbeidet.

Steg 5 er gjennomført med kompatibilitetsavgrensningen ovenfor. Neste planlagte steg er praktisk kontroll på en egen Windows-standardkonto: låsing, opplåsing, nettbrudd, søvn, brukerbytte og oppdatering. Det krever en synlig testøkt og inngår ikke i denne kodeoppryddingen.
