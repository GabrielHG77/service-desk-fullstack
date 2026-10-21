# Fator de importação

Varre as pastas de pedidos de importação, extrai valores das notas de débito em PDF
e do arquivo de materiais, e consolida em `ConsolidadoPath.xlsx` com duas abas
(`AIR` e `SEA`).

**Fator de importação** = custo total ÷ valor CIF. A faixa esperada é 24%–30%.
Pedido pequeno estoura a faixa e isso é normal: despesa fixa por processo
(honorários, despesas na origem) não acompanha o valor da carga.

## Como rodar

Dois jeitos, mesmo resultado:

```
npm start                  # precisa de Node 22+
dist\fator-importacao.exe  # executável único, pasta portátil, sem instalação
```

Abre o navegador em `http://127.0.0.1:7317`. Uma tela só: caminhos no topo,
meses encontrados com quantos pedidos são novos, e dois botões —
**Conferir sem gravar** (roda tudo e mostra o resultado sem tocar na planilha)
e **Consolidar N pedidos** (grava).

Antes de gravar, a planilha é copiada para `ConsolidadoPath.bak.xlsx`.

## Configuração

`config.json` fica ao lado do executável (ou no diretório atual, rodando por `npm`).
É criado na primeira vez que você troca um caminho na tela.

```json
{ "raiz": "Y:\\BR10\\Procurement\\Public\\Teste fator importação\\" }
```

- `raiz` — pasta de rede com a subpasta `dados`. Também aceita `--raiz=...` na linha de comando.
- `planilha` — opcional. Ausente significa `<raiz>\ConsolidadoPath.xlsx`; trocar a
  raiz volta a derivar daí.

A hierarquia esperada dentro da raiz tem exatamente três níveis:

```
dados\<mês>\<modal>\<pedido>\
```

`<modal>` é `AIR` ou `SEA`, em qualquer caixa. Cada pasta `<pedido>` tem **um**
arquivo de materiais (`.xlsx` ou `.csv`) e vários PDFs de notas de débito.
O nome do arquivo de materiais sem a extensão é a **N/Ref.**, a chave do pedido.

## Build do executável

```
npm run build    # bun build --compile --target=bun-windows-x64 -> dist\fator-importacao.exe
```

Precisa do [Bun](https://bun.sh) na máquina de build, não na de uso. O `.exe` é
autocontido: não precisa de Node, de instalação nem de privilégio de administrador.
A tela é embutida no binário por `scripts/ui.mjs`.

## Decisões que não são óbvias

**Excel nunca é aberto.** `.xlsx`, `.csv` e `.pdf` são lidos por biblioteca
(`exceljs`, `pdfjs-dist`), direto do arquivo. Automatizar o Excel era de onde
vinham os travamentos em caixas de diálogo.

**Nada depende da configuração regional.** A máquina está em inglês e os dados
são brasileiros. Separador decimal, separador de campo e formato de data são
explícitos no código (`src/util.js`). No CSV legado o separador decimal é um
**TAB** (`668<TAB>14` = 668,14) e o de campo é `;`.

**Só II e Tx. Siscomex entram no custo.** PIS, COFINS e ICMS são recuperáveis —
a empresa credita depois. Incluí-los fazia o fator aparecer entre 69% e 200%.

**Armazenagem soma todas as cobranças.** Uma nota traz uma linha
`Armazenagem - destino` por período de permanência. Pegar só a primeira dá um
número plausível e errado.

**A coluna `Outros` fica sempre vazia.** É onde a analista digita à mão uma
despesa não contemplada. `Total` e `Percentual` são fórmulas do Excel que incluem
`Outros`, então o total acompanha o que ela digitar.

**Reexecução não duplica.** A N/Ref. na coluna A é a chave: pedido já presente é
pulado inteiro. Regravar um mês é uma ação explícita em "Histórico", nunca efeito
colateral de marcar o mês.

**Pasta que falha não derruba a execução.** Arquivo truncado ou aberto por outra
pessoa vira erro registrado com o motivo; o resto continua. A lista sai na tela e
em `logs\run-<data>.txt`.

**Fator logístico.** Além do fator cheio, o relatório traz o custo sem II e sem
Tx. Siscomex dividido pelo CIF. É a parcela sobre a qual uma decisão de compra ou
de despachante muda alguma coisa — o fator cheio esconde a diferença entre os
modais porque a alíquota de imposto é praticamente a mesma nos dois.

## Testes

```
npm test
```

Cobrem as armadilhas conhecidas: TAB como decimal, ano de dois dígitos, hora
grudada na data, dia/mês trocados, armazenagem com múltiplas cobranças, filtro de
extrato e de despachante, razão social com a palavra ARMAZENAGEM, coluna `Pedido`
duplicada, linhas de material em branco, e um teste ponta a ponta que gera PDFs e
planilhas sintéticas e confere o `ConsolidadoPath.xlsx` resultante célula a célula.

**Ainda não conferido contra os dados reais.** Os números de validação do
levantamento (27 pedidos AIR / 16 SEA, fator ponderado 43,8% / 28,8%, honorários
AIR sempre R$ 1.417,79) só podem ser reproduzidos rodando contra a pasta de rede.
