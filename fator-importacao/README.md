# Fator de importação

Varre as pastas de pedidos de importação, extrai valores das notas de débito em
PDF e do arquivo de materiais, e consolida em `ConsolidadoPath.xlsx` com duas
abas (`AIR` e `SEA`).

**Fator de importação** = custo total ÷ valor CIF. A faixa esperada é 24%–30%.
Pedido pequeno estoura a faixa e isso é normal: despesa fixa por processo
(honorários, despesas na origem) não acompanha o valor da carga.

Mesmo formato do `mqtt-panel`: um executável de ~12 MB, baixado de `dist/`,
sem instalação e sem privilégio de administrador.

## Como rodar

Baixe `dist/fator-importacao.exe`, ponha numa pasta qualquer e dê dois cliques.

Abre em **janela própria**: sem barra de endereço, sem abas, ícone próprio na
barra de tarefas, e fechar a janela encerra o app. Abrir o executável de novo
com o app já aberto só traz a janela existente para a frente.

Uma tela só: caminhos no topo, meses encontrados com quantos pedidos são novos,
e dois botões — **Conferir sem gravar** (roda tudo e mostra o resultado sem
tocar na planilha) e **Consolidar N pedidos** (grava).

Antes de gravar, a planilha é copiada para `ConsolidadoPath.bak.xlsx`.

Prefira uma pasta local (`C:\Users\<você>\Apps\FatorImportacao`) a uma pasta do
OneDrive: o app grava `config.json` e `logs\run-*.txt` ao lado do executável, e
sincronizar isso a cada execução só atrapalha.

## Configuração

`config.json` fica ao lado do executável e é criado na primeira vez que você
troca um caminho na tela.

```json
{ "raiz": "Y:\\BR10\\Procurement\\Public\\Teste fator importação\\" }
```

- `raiz` — pasta de rede com a subpasta `dados`. Também aceita `-raiz=...` na
  linha de comando.
- `planilha` — opcional. Ausente significa `<raiz>\ConsolidadoPath.xlsx`;
  trocar a raiz volta a derivar daí.

Outros parâmetros: `-porta` (padrão 7317), `-config`, `-sem-janela`.

A hierarquia esperada dentro da raiz tem exatamente três níveis:

```
dados\<mês>\<modal>\<pedido>\
```

`<modal>` é `AIR` ou `SEA`, em qualquer caixa. Cada pasta `<pedido>` tem **um**
arquivo de materiais (`.xlsx` ou `.csv`) e vários PDFs de notas de débito.
O nome do arquivo de materiais sem a extensão é a **N/Ref.**, a chave do pedido.

## Build

```
go test ./...
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H=windowsgui" -o dist/fator-importacao.exe .
```

`-H=windowsgui` é o que tira a janela preta de terminal. A tela é embutida no
binário por `go:embed`, então o `.exe` é autocontido.

Para pôr um ícone no arquivo `.exe` (e não só na janela) é preciso compilar no
próprio Windows, com um `.syso` de recursos. Hoje o ícone vem do favicon
embutido em `ui.html`, que é o que aparece na barra de tarefas.

## Regras de cálculo editáveis

A aba **Regras de cálculo** muda duas coisas, gravadas em `config.json`:

- **A faixa esperada.** O padrão é 24% a 30%. Fora dela o pedido entra na lista
  de avisos e aparece destacado no gráfico — nunca impede a gravação.
- **Quais componentes entram no custo.** Os oito que o app extrai: II, Tx.
  Siscomex, frete rodoviário, marinha mercante, despesas na origem, armazenagem,
  honorários e frete internacional. Desligar um deles muda o Total, o Percentual
  e a fórmula gravada na planilha; a coluna `Outros` entra sempre.

```json
{ "faixa": { "min": 0.24, "max": 0.30 },
  "custo": ["ii","siscomex","rodoviario","marinha","origem","armazenagem","honorarios","freteIntl"] }
```

Com todos ligados a fórmula sai contígua (`SUM(L4:T4)`); desligando um componente
do meio ela vira soma explícita (`L4+Q4+T4`). Configuração editada à mão com
chave desconhecida ou faixa invertida volta ao padrão em vez de derrubar o app.

Vale só para as próximas consolidações: o que já está gravado só muda ao
reprocessar o mês.

**PIS, COFINS e ICMS não estão na lista de propósito.** São recuperáveis — a
empresa credita depois — e o app nem chega a ler essas colunas do arquivo de
materiais. Incluí-los já fez o fator aparecer entre 69% e 200%.

## Gráficos

A aba **Gráficos** lê o `ConsolidadoPath.xlsx` inteiro, não só a última execução:

- resumo com o fator ponderado de tudo, do marítimo e do aéreo;
- fator ponderado por mês, lado a lado por modal, sobre a faixa;
- de onde vem o custo: quanto é imposto e quanto é logística em cada modal;
- cada pedido sobre a faixa, ordenado por valor de carga.

O Total é recalculado somando as colunas de custo em vez de lido da coluna
`Total`: lá mora uma fórmula, cujo valor só fica em cache depois que o Excel
abre o arquivo. O mês vem da data das linhas de material, porque a linha de
total não tem data.

**Exportar PDF** abre a janela de impressão do navegador — escolha "Salvar como
PDF". O CSS de impressão esconde a barra lateral e os botões, força retrato e
evita cortar card ao meio; o cabeçalho da folha diz qual planilha, qual faixa e
quando foi gerado.

## Decisões que não são óbvias

**Excel nunca é aberto.** `.xlsx`, `.csv` e `.pdf` são lidos por biblioteca
(`excelize`, `ledongthuc/pdf`), direto do arquivo. Automatizar o Excel era de
onde vinham os travamentos em caixas de diálogo.

**Nada depende da configuração regional.** A máquina está em inglês e os dados
são brasileiros. Separador decimal, separador de campo e formato de data são
explícitos em `util.go`. No CSV legado o separador decimal é um **TAB**
(`668<TAB>14` = 668,14) e o de campo é `;`.

**O `.xlsx` é lido cru.** `GetRows` normalmente devolve a data já formatada pelo
número de formato do arquivo — se ele estiver em `mm/dd/yyyy`, 3 de agosto
chegaria como `08/03/2026` e viraria 8 de março em silêncio. Com
`RawCellValue` a data vem como número de série, sem ambiguidade. Em troca, os
números vêm em formato de máquina, e por isso o `.csv` e o `.xlsx` usam
conversores diferentes.

**Só II e Tx. Siscomex entram no custo.** PIS, COFINS e ICMS são recuperáveis —
a empresa credita depois. Incluí-los fazia o fator aparecer entre 69% e 200%.

**Armazenagem soma todas as cobranças.** Uma nota traz uma linha
`Armazenagem - destino` por período de permanência. Pegar só a primeira dá um
número plausível e errado.

**Os regex viraram grupo de captura.** O spec usa lookbehind, que o motor de
expressão regular do Go não tem. O corpo do padrão é idêntico e a semântica é a
mesma: acha o rótulo, pega o primeiro número depois dele.

**A coluna `Outros` fica sempre vazia.** É onde a analista digita à mão uma
despesa não contemplada. `Total` e `Percentual` são fórmulas do Excel que
incluem `Outros`, então o total acompanha o que ela digitar.

**Reexecução não duplica.** A N/Ref. na coluna A é a chave: pedido já presente é
pulado inteiro. Regravar um mês é uma ação explícita em "Histórico", nunca
efeito colateral de marcar o mês.

**Pasta que falha não derruba a execução.** Arquivo truncado ou aberto por outra
pessoa vira erro registrado com o motivo; o resto continua. A lista sai na tela
e em `logs\run-<data>.txt`.

**Fator logístico.** Além do fator cheio, o relatório traz o custo sem II e sem
Tx. Siscomex dividido pelo CIF. É a parcela sobre a qual uma decisão de compra
ou de despachante muda alguma coisa — o fator cheio esconde a diferença entre
os modais porque a alíquota de imposto é praticamente a mesma nos dois.

## A casca é web, a janela não é um navegador

A tela é HTML servido por um servidor HTTP que só aceita requisição de
`127.0.0.1` — nada sai da máquina. O app abre essa tela em modo aplicativo
(`--app`) no Edge ou no Chrome que já existem no Windows corporativo, com um
perfil separado em `%LOCALAPPDATA%\FatorImportacao\janela`. É a mesma abordagem
do `mqtt-panel` (`main.go`, função `open`), e procura o navegador nas mesmas
três pastas — `Program Files (x86)`, `Program Files` e `LocalAppData`, esta
última porque numa máquina sem admin o Edge fica instalado por usuário.

## Testes

```
go test ./...
```

Cobrem as armadilhas conhecidas: TAB como decimal, ano de dois dígitos, hora
grudada na data, dia/mês trocados nos dois formatos de entrada, armazenagem com
múltiplas cobranças, filtro de extrato e de despachante, razão social com a
palavra ARMAZENAGEM, coluna `Pedido` duplicada, linhas de material em branco, e
um teste ponta a ponta que gera PDFs comprimidos e planilhas sintéticas e
confere o `ConsolidadoPath.xlsx` resultante célula a célula.

**Ainda não conferido contra os dados reais.** Os números de validação do
levantamento (27 pedidos AIR / 16 SEA, fator ponderado 43,8% / 28,8%, honorários
AIR sempre R$ 1.417,79, despesas na origem SEA só R$ 273,50 ou R$ 564,11) só
podem ser reproduzidos rodando contra a pasta de rede. O ponto mais frágil é a
extração de texto do PDF: a reconstrução de layout foi calibrada em PDF
sintético, não nas notas do agente de carga.
