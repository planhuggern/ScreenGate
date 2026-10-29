# Regler for telling og tilgang

Status: **steg 1 fullført med avgrensning for avlesingsfeil**. Dokumentet beskriver ønsket oppførsel etter kommende endringer, ikke hva dagens klient allerede garanterer. Nettbrudd, gjenoppkobling, døgnskifte, tidsplan, flere PC-er, oppstart uten regler og avvist nøkkel er avklart med brukeren. Ved feil i avlesing av Windows-status er beslutningen å samle observasjoner først. Ingen ny regel for telling eller låsing ved slike feil innføres nå. Ingen programkode endres i dette steget.

Grunnlag: [tilstandsrapporten](STATE_REPORT.md), datert 24. oktober 2026.

## Vedtatte hovedvalg

| Valg | Regel eller forslag | Konsekvens |
| --- | --- | --- |
| A. Nettbrudd — vedtatt | Siste gyldige restkvote gjelder ved nettbrudd, serverkrasj og avslått nett. Klienten beregner og trekker forbruk lokalt. | Kontaktfeil alene utløser ikke låsing etter 90 sekunder. Når restkvoten er brukt opp, skal klienten håndheve stenging i låsemodus. |
| B. Oppstart uten lagrede regler — vedtatt | Kontakt serveren umiddelbart ved oppstart og hvert forsøk på gjenopplåsing. Hvis regler ikke kan hentes, lås igjen. Oppdater når kontakt oppnås. | Ingen 30-sekunders oppstartsfrist. Uten lagrede regler holdes tilgangen stengt til regler er hentet. |
| C. Feil ved avlesing av Windows-status — vedtatt avgrensning | Registrer avlesingsfeil tydelig i klientloggen og overvåkingen. Undersøk faktiske observasjoner før en særskilt håndtering velges. | Forslaget om å telle som ulåst og låse etter 30 sekunder er trukket tilbake. Dagens feiloppførsel dokumenteres og testes før eventuelle endringer. |

Valg C vedtar observasjon som første tiltak. Det vedtar ikke dagens tolkning av avlesingsfeil som «låst» som en permanent produktregel.

## 1. Ansvar og begreper

**Serveren er master når det er kontakt. Klienten viderefører kvoten lokalt uten kontakt.** Serveren fastsetter kvote og foreldreregler. Når kontakten mangler, håndhever klienten siste gyldige restkvote og trekker forbruk lokalt. Ved gjenoppkobling mottar serveren oppdatert restkvote fra klienten, avstemmer forbruket og overtar som master igjen. Et eldre serversvar skal ikke erstatte klientens oppdaterte verdi før avstemmingen.

**Klienten observerer Windows og håndhever tilgangen.** Følgende forhold holdes atskilt:

- Øktstatus: bekreftet ulåst eller bekreftet låst. Feil ved avlesing registreres særskilt som diagnostikk.
- Forbindelse: serverkontakt lykkes eller feiler.
- Tilgang: sist tillatt bruk med restkvote, eksplisitt stenging, oppbrukt kvote eller avventende oppstart.
- Modus: testmodus eller låsemodus.

«Ingen serverkontakt» betyr ikke «ingen tidligere beslutning». En feil ved avlesing av Windows-status er ikke bekreftelse på låst økt. Registrering av en slik feil skal ikke i seg selv innføre en ny regel for tilgang eller belastning.

## 2. Tidsberegning og lokal restkvote

- Heartbeat sendes hvert 30. sekund og ved endret øktstatus, også mens Windows er låst.
- Ved kontakt er serverens regnskap autoritativt. For eldre klienter bruker serveren mottakstidspunkter. Nye klienter sender målte intervaller som serveren bekrefter og tar med én gang, også ved gjenoppkobling.
- For eldre klienter belaster serverberegningen et positivt intervall på høyst 63 sekunder hvis forrige rapporterte øktstatus var ulåst. En låserapport avslutter intervallet; en opplåsingsrapport starter et nytt.
- Ulåst video og bruk uten tastatur- eller museaktivitet teller. Inaktivitet alene fritar ikke for belastning.
- Klienten måler ulåst bruk lokalt og reduserer sist kjente restkvote selv uten kontakt. Bekreftet låst økt og søvn skal ikke bruke lokal kvote. Måleoppførsel ved avlesingsfeil omfattes av den uttrykkelige avgrensningen i valg C; ingen ny belastningsregel er vedtatt for dette.
- For eldre klienter utelater serveren mottaksintervaller over 63 sekunder. Denne grensen skal ikke stoppe klientens lokale opptelling mens barnet bruker PC-en uten nett. Det tidligere forslaget om ingen etterregistrering er ikke lenger et tilstrekkelig regelgrunnlag: lokalt forbruk må avstemmes ved gjenopprettet kontakt.
- Forbruk summeres per bruker. To PC-er i samtidig bruk gir belastning fra begge. Kalenderdager følger serverens konfigurerte tidssone.
- Samme rapport-ID belastes bare én gang. Endret øktstatus får ny ID. Et duplikat kan få en ny serverbeslutning basert på gjeldende regler, men gir ikke nytt bruksintervall.

Pause, ukeplan, bonus og kvote beholder dagens prioritet: pause, deretter tidsplan, deretter dagskvote med dagens bonus. Null kvote betyr ubegrenset dagskvote; pause og tidsplan gjelder fortsatt.

## 3. Tilgang ved kontaktfeil og omstart

**Vedtatt:** Nettbrudd, serverkrasj og avslått nett gir ikke ny kvote og fjerner ikke gjenværende kvote. Klienten fortsetter fra siste gyldige restkvote og trekker faktisk bruk lokalt. Utløp av dagens 90-sekunders tillatelse skal ikke alene stenge videre bruk uten kontakt.

Eksempel: Klienten har fått 25 minutter igjen og mister kontakten. Etter 10 minutter ulåst bruk har den 15 minutter igjen, selv om serveren fortsatt er utilgjengelig. Den får verken fri bruk eller en ny full dagskvote.

Lokal restkvote og ikke avstemt forbruk lagres slik at klientomstart ikke fyller opp kvoten eller mister forbruk. Klienten skal ikke belaste søvn eller tiden prosessen ikke kjørte som om det var målt ulåst bruk. Detaljer ved lokal klokkeendring og lagringsfeil må beskrives ved utforming av tilstandslagringen; de gir ikke grunnlag for å fylle opp en kjent restkvote.

Timeout, HTTP 400/405/429/503, omdirigering og ugyldige svar gir ingen brukbar oppdatering: siste gyldige regler beholdes og lokal kvote telles videre. En aktiv pause eller oppbrukt kvote oppheves ikke av kontaktfeil. En lagret tidsplan kan derimot nå et nytt tillatt tidsrom; klienten vurderer sist kjente regler lokalt.

HTTP 401/403 ugyldiggjør tilgangen umiddelbart i låsemodus, også ved oppstart og etter tidligere kontaktfeil. Bare en ny, gyldig serverbeslutning med tillatelse kan åpne tilgangen igjen.

Ved oppstart med lagrede regler og restkvote videreføres disse med registrert lokalt forbruk. Nettbruddet eller omstarten gir ikke ny kvote på samme dag. Ved døgnskifte gjelder den lagrede ukeplanen, som beskrevet nedenfor. En aktiv pause, avvist nøkkel eller oppbrukt kvote holdes stengt etter gjeldende regler.

Uten lagrede regler prøver klienten å kontakte serveren umiddelbart ved oppstart og ved hvert forsøk på gjenopplåsing. Hvis den ikke får hentet regler, forsøker den å låse igjen. Den fortsetter også vanlige kontaktforsøk mens økten er låst. Når kontakt oppnås, hentes regler og tilgang vurderes fra dem. Klienten skal ikke finne på en kvote eller gi en oppstartsfrist.

Det innføres ingen egen tidsfrist ved feil i avlesing av Windows-status. Hvordan eldre tilstandsfiler oppgraderes avgjøres før implementering, uten å tolke ukjent historikk som en ny kvote.

Et gyldig svar med `allow` kan gjøre videre bruk tillatt igjen. Det låser ikke automatisk opp Windows; brukeren låser selv opp økten.

### Gjenoppkobling: serveren overtar som master

1. Klienten sender oppdatert gjenværende kvote og opplysninger som trengs for avstemming av det lokale forbruket.
2. Serveren tar med denne oppdateringen én gang. Forbruk fra samme tidsrom skal ikke trekkes både som serverintervall og som lokalt forbruk, og gjentatte forsøk skal ikke trekke det på nytt.
3. Serveren overtar som master og returnerer oppdatert restkvote og gjeldende foreldreregler. Klienten går tilbake til å følge serverens oppdateringer.

Eksempel med én PC og uendrede regler: Sist delte restkvote var 25 minutter. Klienten bruker 10 minutter uten nett og melder 15 minutter igjen. Serveren overtar med 15 minutter, ikke 25 eller 5.

Hvis andre enheter også har brukt kvoten, eller foreldrereglene er endret under nettbruddet, må serveren samordne disse opplysningene. En klients restkvote skal ikke slette en annen klients registrerte forbruk. Detaljene i rapportformat, bekreftelser og perioder er en senere implementeringsoppgave.

### Døgnskifte, tidsplan og flere enheter

- **Døgnskifte:** Ny dagskvote følger sist nedlastede ukeplan i serverens konfigurerte tidssone. Bonus fra gårsdagen utløper. Forbruk fra ulike kalenderdager holdes atskilt ved senere avstemming.
- **Tidsplan og pause:** Sist kjente regler håndheves lokalt. Nye foreldreendringer gjelder på klienten når de mottas. En pause uten avtalt slutt videreføres til en ny regel åpner tilgangen.
- **Flere PC-er:** Hver frakoblede klient bruker sist kjente restkvote. Midlertidig overforbruk av felleskvoten aksepteres under nettbrudd. Ved gjenoppkobling summerer serveren faktisk forbruk fra enhetene uten å trekke samme forbruk flere ganger.
- **Eksempel med to PC-er:** Begge starter fra 30 minutter igjen. A bruker 10 minutter og melder 20 igjen. B bruker 5 minutter og melder 25 igjen. Felles restkvote blir 15 minutter, ikke den sist mottatte verdien 20 eller 25.
- **Ubegrenset kvote:** Tidsplan og pause gjelder selv om dagskvoten er ubegrenset. Nettbrudd alene innfører ingen ny tidsgrense.

Klienten må derfor ha mer enn et resttall: den trenger lagrede regler og et grunnlag for å knytte lokalt forbruk til riktig dag og siste avstemming. Hvordan dette lagres og overføres velges i senere steg.

## 4. Feil ved avlesing av Windows-status: observer først

**Forklaring:** Klienten leser øktstatus gjennom Windows-kall som `ProcessIdToSessionId`, `WTSQuerySessionInformationW` og `OpenInputDesktop`. Hvis et kall feiler, ikke gir noe svar eller gir et svar klienten ikke kan tolke, vet klienten ikke sikkert om økten er låst. Dette er en avlesingsfeil, ikke en egen modus brukeren har valgt. Dagens kode returnerer `locked` også for flere slike feil; for eksempel gir et mislykket kall til `WTSQuerySessionInformationW` samme resultat som bekreftet låst økt. Vi har ikke påvist at en slik feil faktisk har oppstått på barnas PC-er.

**Vedtatt tiltak for senere implementering:**

- Klientloggen skal vise hvilket kall eller hvilken tolkning som feilet, tidspunkt og eventuell feilkode fra Windows. Hvis ingen feilkode finnes, registreres årsaken uten å finne på en kode.
- Overvåkingen skal vise at klienten rapporterer en avlesingsfeil, og skille denne fra nettbrudd og en bekreftet låst økt. Sist vellykkede avlesing kan vises som historikk med tidspunkt, ikke som bekreftelse på nåværende status.
- Ved kontaktbrudd skal feilen fortsatt kunne finnes i den lokale loggen. Transport og eventuell senere rapportering beskrives ved implementering.
- En feil skal ikke alene utløse en ny 30-sekundersfrist, automatisk låsing eller en ny regel om å telle økten som ulåst.
- Dagens fallback og dens konsekvenser for telling og låsing skal gjengis i testene fra steg 2. En endret fallback krever et nytt, uttrykkelig regelvalg på grunnlag av observasjonene.

Dette er en bevisst avgrensning: avlesingsfeil er ikke påvist på barnas PC-er, og den langsiktige feilregelen er utsatt. At øvrige regler er avklart betyr ikke at den nåværende klienten allerede håndhever dem ved alle avlesingsfeil.

## 5. Testmodus og stoppet klient

Testmodus vurderer og rapporterer de samme reglene, men kaller aldri Windows-låsing. Den skal tydelig vise når klienten ville ha låst. Vi beholder dagens standard med testmodus ved installasjon; automatisk oppdatering beholder eksisterende modus.

Reglene for håndheving forutsetter at klienten kjører. En stoppet prosess kan ikke låse Windows. Beskyttelse mot avslutning behandles som et eget senere prosjekt. Manglende rapporter skal ikke presenteres som bevis for krasj eller bevisst omgåelse.

## 6. Eksempler med ett forventet utfall

Eksemplene bruker de avklarte reglene i låsemodus, med mindre annet er oppgitt. Radene om avlesingsfeil beskriver observasjonstiltaket og avgrensningen i C. «Lås» betyr at klienten skal forsøke Windows-låsing; faktisk resultat må observeres.

| Situasjon | Forventet utfall |
| --- | --- |
| Første rapport mottas 12:00:00 med ulåst økt. Neste nye rapport mottas 12:00:30. | Første rapport belaster 0 sekunder. Neste belaster 30 sekunder. |
| Ulåst rapport 12:00:00, låst rapport 12:00:10, fortsatt låst 12:00:40. | Henholdsvis 0, 10 og 0 sekunder ny belastning. |
| Låst rapport 12:00:00, ulåst rapport 12:00:10, fortsatt ulåst 12:00:40. | Henholdsvis 0, 0 og 30 sekunder ny belastning. |
| Ny rapport kommer 64 sekunder etter forrige. | Eldre klienters mottaksberegning utelater intervallet. Nye klienters lokalt målte forbruk avstemmes; en forsinket rapport gir ikke gratis lokal bruk. |
| En rapport prøves igjen med samme ID. | Ingen ny belastning. Serveren vurderer dagens regler på nytt. |
| Klienten har 25 minutter igjen og mister nettverket. Barnet bruker PC-en ulåst i 10 minutter. | Lokal restkvote er 15 minutter. Ingen låsing bare fordi 90 sekunder er gått. |
| Samme utgangspunkt; barnet bruker alle de 25 minuttene uten nettverk. | Restkvoten er null og klienten forsøker å låse. |
| Samme utgangspunkt; barnet bruker 10 minutter og låser deretter Windows i 5 minutter. | Restkvoten er fortsatt 15 minutter etter den låste perioden. |
| Klienten startes om etter å ha brukt 10 av de 25 minuttene uten nettverk. | Restkvoten skal fortsatt være 15 minutter, ikke 25 minutter. |
| Kontakten kommer tilbake etter 10 minutter lokal bruk fra en restkvote på 25 minutter; ingen andre enheter eller regelendringer. | Klienten sender 15 minutter igjen. Serveren avstemmer og overtar som master med 15 minutter. |
| Sist kjente tidsplan når slutt på tillatt tidsrom under nettbrudd. | Klienten forsøker å låse etter den lagrede tidsplanen, selv om det er restkvote igjen. |
| Serveren har sagt lås. Neste rapport feiler på nettverket. | Tilgangen forblir stengt. |
| En ny klient uten lagrede regler starter uten nettverk. | Forsøk serverkontakt umiddelbart. Hvis regler ikke kan hentes, forsøk låsing igjen. Ingen oppstartsfrist på 30 sekunder. |
| Barnet forsøker å låse opp igjen mens klienten fortsatt mangler regler. | Gjør et nytt kontaktforsøk umiddelbart. Lås igjen hvis regler fortsatt ikke kan hentes. |
| Serveren blir tilgjengelig mens klienten mangler regler. | Hent regler og vurder tilgang. Et gyldig `allow` tillater videre bruk, men låser ikke automatisk opp Windows. |
| Klienten starter med lagret restkvote, men dagens 90-sekunders tillatelse har utløpt. | Ikke lås bare på grunn av dette utløpet. Bruk bevart restkvote og lagrede regler for den aktuelle dagen. |
| Midnatt passeres uten nett, med lagret ukeplan. | Start den nye dagens kvote etter ukeplanen. Gårsdagens bonus utløper; tidsplan og eventuell pause gjelder fortsatt. |
| HTTP 401/403 mottas under oppstart eller etter nettfeil. | Tilgangen stenges straks, uansett tidligere tilstand. |
| Et Windows-kall for avlesing av øktstatus feiler 12:00:10. | Registrer tidspunkt, kall og tilgjengelig feilårsak i lokal logg og overvåking. Ingen ny regel for telling eller låsing aktiveres av observasjonstiltaket. |
| Avlesingsfeilen vedvarer i mer enn 30 sekunder. | Fortsett å registrere feiltilstanden. Ingen særskilt 30-sekunders låseregel innføres. |
| Avlesingen feiler fra 12:00:10 og lykkes igjen 12:00:15. | Logg at avlesingen fungerer igjen og vis en fersk, bekreftet status. Feilperioden beholdes som diagnostikk. |
| Klienten får et gyldig `allow` etter et nettbrudd og et låseforsøk. | Tilgang er igjen tillatt innenfor den nye beslutningen. Windows låses ikke automatisk opp. |
| Testmodus møter oppbrukt kvote, nettbrudd eller avlesingsfeil. | Samme beslutning og feilårsak registreres, men Windows-låsing kalles aldri. |
| Klientprosessen avsluttes. | Serveren ser etter hvert manglende rapporter. Håndheving kan ikke garanteres før klienten kjører igjen. |

## Avgrensning og neste steg

Steg 1 er fullført som beslutningsgrunnlag, med den uttrykkelige avgrensningen om å observere avlesingsfeil før eventuell endret håndtering. Valg A erstatter anbefalingen om låsing ved tillatelsens utløp i den opprinnelige tilstandsrapporten. Valg B erstatter forslaget om 30 sekunders oppstartsfrist. Valg C erstatter forslaget om å telle som ulåst og låse etter 30 sekunders avlesingsfeil. README, API-dokumentasjonen og driftsveiledningen er ikke oppdatert til å påstå at de nye reglene allerede er implementert. De kjente motsetningene står i tilstandsrapporten.

Steg 2 er fullført: [dagens klientflyt og dokumenterte avvik](STEP_2.md) beskriver testene, inkludert dagens fallback ved avlesingsfeil. De vedtatte reglene er ikke innført gjennom dette testarbeidet. Lokal kvotelagring, avstemming, rapportering av avlesingsfeil og overgang fra gamle klientfiler må løses i senere steg; dette dokumentet velger ikke implementasjonen.

Senere implementeringsstatus: [Steg 4](STEP_4.md) innfører lokal videreføring av siste restkvote, bevarer kjent stengetid og stenging ved kontaktfeil, og stenger uten tidligere gyldig beslutning eller ved HTTP 401/403. Del 2 innfører bekreftet avstemming ved gjenoppkobling. Del 3 innfører lagret ukeplan og ny dagskvote ved midnatt uten nett. Del 4 innfører observasjon av Windows-avlesingsfeil i lokal logg og overvåking, med dagens reserveverdier beholdt. Praktisk Windows-kontroll gjenstår.
