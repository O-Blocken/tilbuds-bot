# Søgesprog

Bottens søgning forstår et lille, deterministisk søgesprog. Det virker
både i `/search` og i ønsker oprettet med `/want add`. Der er ingen AI
eller gætteri bag — de samme ord giver altid de samme resultater.

## Grundlag

Almindelige ord matcher hele ord i tilbuddets navn:

```text
øl
hakket oksekød
```

Flere ord skal **alle** matche (OG). Sæt ord i anførselstegn, når de
skal stå ved siden af hinanden som en fast vending:

```text
"coca cola"        ← kun Coca Cola
hakket oksekød     ← både "hakket" og "oksekød" (kan stå hver for sig)
```

## Udelukkelser

Sæt `-` foran et ord eller en vending for at udelukke tilbud, der
matcher det. Udelukkelser er altid stærkere end positive ord:

```text
øl -alkoholfri -julekalender
"coca cola" -zero
ost -økologisk
```

## Synonymer (koncepter)

Nogle ord er registreret med synonymer. Skriver du `øl`, finder botten
også tilbud, der kun skriver `pilsner`, `lager` eller et ølmærke som
`Carlsberg`. Skriver du `cola`, finder den også `coca cola` og `pepsi`.

Et synonymslag virker også baglæns: søger du `pilsner`, rammer du også
`øl`. Synonymer gælder også for udelukkelser: `-øl` afviser også
`pilsner` og `Carlsberg`.

Eksempler på registrerede koncepter: øl, cider, sodavand, cola,
coca-cola, pepsi, juice, kaffe, te, vin, spiritus, mælk, yoghurt, ost,
smør, fløde, æg, kylling, oksekød, svinekød, lammekød, kalvekød, bacon,
and, pålæg, laks,
fisk, rejer, pasta, ris, brød, kartofler, frugt, grøntsager, snacks,
slik, chokolade, is, kage, pizza, suppe, færdigretter, frost, økologisk,
julekalender, julefrokost, nytår, dansk, delikatesse, bager — plus
mærker som Haribo, Kinder, Tulip, Danpo, Arla, Cheasy og mange flere.

Bemærk: skriver du `tuborg`, rammer du også `Tuborg Squash` (sodavand).Vil du kun have øl, så skriv `øl tuborg` eller `øl -sodavand`.

## Filtre

| Filter | Betydning | Eksempel |
| --- | --- | --- |
| `price:` | Varens pris | `price:<50`, `price:<=100`, `price:>=20` |
| `max:` | Kort for `price:<=` | `max:50` |
| `min:` | Kort for `price:>=` | `min:25` |
| `unitprice:` | Stykpris (kg/l/stk) | `unitprice:kg:<80`, `unitprice:l:<=12` |
| `discount:` | Rabat i procent | `discount:>=25%` |
| `store:` | Butik | `store:føtex`, `store:spar`, `store:lidl` |
| `dept:` | Afdeling | `dept:kød`, `dept:frugt`, `dept:frost` |
| `app:` | App-tilbud til/fra | `app:false` (spring app-tilbud over) |

Sammenligningsoperatorer: `<` `<=` `=` `>=` `>`. Uden operator betyder
`=`. Priser kan skrives med komma eller punktum: `price:<=49,95`.

Tilbud uden prisdata matcher **ikke** en søgning, der kræver en
pris sammenligning — f.eks. rammer `price:<50` aldrig et tilbud uden
pris. Det gælder `/search` og ønsker oprettet med `/want add`, hvor
tekstfeltet indeholder søgesprog (fx id `cola`, tekst `cola price:<20`).
Gamle ønsker, der kun består af
inkluder-/ekskluder-ord (fx importeret fra `keywords`), lader tilbud uden
prisdata passere, så manglende data aldrig skjuler et tilbud.

En søgning, der ikke selv nævner pris, pris pr. enhed eller rabat, er
ikke påvirket: `cola` finder også tilbud uden pris.

### Eksempler

```text
kaffe max:50
oksekød unitprice:kg:<80
øl discount:>=25%
cola store:føtex -zero
slik -julekalender max:30
frost dept:frost app:false
```

## Godt at vide

- Ord matcher hele ord: `is` rammer ikke `gris`, og `øl` rammer ikke
  `pølser`.
- Længere ord finder også sammensatte ord: `chokolade` rammer
  `chokoladebar`.
- Danske bogstaver omskrives automatisk (`ø` ≡ `oe`), så `snøfler`
  også finder `snoefler`.
- Skriv `"` for vendinger. En uafsluttet `"` giver en fejl — søgningen
  afvises i stedet for at gætte.
- Ukendte filtre (f.eks. `colour:rød`) giver en fejl med listen over
  gyldige filtre.

## Fejlmeddelelser

Ugyldige søgninger afvises altid med en kort dansk forklaring, f.eks.
manglende `"`, ugyldigt tal eller ukendt filter. En afvist søgning
matcher aldrig halvt — den bliver ikke brugt, før den er rettet.

## Administration

To admin-kommandoer udvider registret og prisvarslerne (kun for
administratorer):

- `/concepts list|show|set|remove|restore|reload` — administrér
  synonymer. `set` gemmer i databasen og gælder med det samme; `remove`
  skjuler et koncept; `restore` nulstiller til `concepts.toml`; `reload`
  genindlæser filen uden genstart.
- `/prisalarm add|list|remove|check` — få besked når et produkt rammer
  en målpris (`pris`) eller underbukker den billigste pris, der nogensinde
  er registreret (uden `pris`). Tjekkes ved hvert nye tilbud og skriver
  højst én gang pr. ugetilbud.

Begge bruger samme søgesprog som `/search` — fx `produkt: coca cola -zero`.
