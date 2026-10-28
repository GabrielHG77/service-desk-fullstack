# Consolidador CBA

App para Windows que faz o mesmo que a consulta do Power Query da pasta `DadosCBA`:
lê a aba **Data** de todos os `.xlsx`/`.xlsm` de uma pasta (com subpastas), transforma
cada característica (`Internal char no.`) em uma coluna e junta tudo em uma tabela,
uma linha por item de ordem de venda.

Mesmo formato do `mqtt-panel` e do `FatorImportacao`: um executável só, baixado de `dist/`,
sem instalador e sem privilégio de administrador.

## Instalar pelo cmd

No **Prompt de Comando** (cmd, não PowerShell), cole e dê Enter:

```bat
mkdir "%LOCALAPPDATA%\Programs\ConsolidadorCBA" 2>nul & cd /d "%LOCALAPPDATA%\Programs\ConsolidadorCBA" && (taskkill /im ConsolidadorCBA.exe /f >nul 2>&1 & ping -n 3 127.0.0.1 >nul & del /f /q ConsolidadorCBA.antigo.exe ConsolidadorCBA.novo.exe >nul 2>&1 & curl -fL -o ConsolidadorCBA.novo.exe https://github.com/GabrielHG77/service-desk-fullstack/raw/refs/heads/claude/understand-query-code-yv85xs/consolidador-cba/dist/ConsolidadorCBA.exe && (move /y ConsolidadorCBA.novo.exe ConsolidadorCBA.exe >nul 2>&1 || (ren ConsolidadorCBA.exe ConsolidadorCBA.antigo.exe && move /y ConsolidadorCBA.novo.exe ConsolidadorCBA.exe >nul)) && ConsolidadorCBA.exe --instalar && echo Pronto: Consolidador CBA instalado.)
```

O comando fecha o app se ele estiver aberto, baixa a versão nova para `ConsolidadorCBA.novo.exe` e só então
troca pelo `ConsolidadorCBA.exe` (se o Windows ainda segurar o antigo, ele é renomeado para
`ConsolidadorCBA.antigo.exe`, que o app apaga quando abrir). O `--instalar` cria os atalhos no menu Iniciar e na
Área de Trabalho, registra o app em **Configurações > Aplicativos instalados** e abre o app. Só para o seu
usuário, sem administrador. Para atualizar, rode o mesmo comando. A versão aparece no pé da barra lateral.

O `--instalar` instala o app na pasta onde o `.exe` está. Para outra pasta, por exemplo junto do `mqtt-panel`,
troque o caminho do `cd`:

```bat
cd /d "C:\Users\<você>\OneDrive - Interroll Management SA\Desktop\MQTT" && (taskkill /im ConsolidadorCBA.exe /f >nul 2>&1 & ping -n 3 127.0.0.1 >nul & del /f /q ConsolidadorCBA.antigo.exe ConsolidadorCBA.novo.exe >nul 2>&1 & curl -fL -o ConsolidadorCBA.novo.exe https://github.com/GabrielHG77/service-desk-fullstack/raw/refs/heads/claude/understand-query-code-yv85xs/consolidador-cba/dist/ConsolidadorCBA.exe && (move /y ConsolidadorCBA.novo.exe ConsolidadorCBA.exe >nul 2>&1 || (ren ConsolidadorCBA.exe ConsolidadorCBA.antigo.exe && move /y ConsolidadorCBA.novo.exe ConsolidadorCBA.exe >nul)) && ConsolidadorCBA.exe --instalar && echo Pronto: Consolidador CBA instalado.)
```

O app não grava nada ao lado do `.exe` (os ajustes ficam em `%APPDATA%\ConsolidadorCBA`), então pode morar
numa pasta do OneDrive sem ficar sincronizando a cada uso.

Para desinstalar: **Configurações > Aplicativos instalados > Consolidador CBA > Desinstalar**, ou no cmd,
`ConsolidadorCBA.exe --desinstalar` na pasta onde ele está. Saem os atalhos, o registro, os ajustes e o
`ConsolidadorCBA.exe`; a pasta e os outros arquivos dela ficam.

## Como usar

1. Baixe `dist/ConsolidadorCBA.exe` (no GitHub, abra o arquivo e clique em **Download raw file**),
   ponha numa pasta qualquer, por exemplo `C:\Users\<você>\Apps\ConsolidadorCBA`, e dê dois cliques.
   Para achar fácil depois, clique com o botão direito no ícone da barra de tarefas e em **Fixar na barra de tarefas**.
2. Clique em **Escolher pasta…** e selecione `Y:\...\Dashboard\2026\DadosCBA`.
   Da próxima vez o app já abre nessa pasta.
3. Clique em **Escolher planilha…**, depois em **Gravar N novos** (veja "A planilha consolidada" abaixo).
   Uma cópia avulsa em Excel ou CSV fica no menu **⋯**.

Atalhos: F5 atualiza, Ctrl+S grava na planilha, Ctrl+F busca, Ctrl+E exporta uma cópia, Ctrl+, abre os Ajustes.

Claro ou escuro: **Ajustes > Aparência > Tema**. Automático segue o tema do Windows; Claro e Escuro valem sempre.

Na primeira vez, o Windows pode mostrar "O Windows protegeu o computador", porque o programa
não é assinado. Clique em **Mais informações** e depois em **Executar assim mesmo**.

## A planilha consolidada

Mesma lógica do `ConsolidadoPath.xlsx` do Fator de Importação:

- **Escolher planilha…** (barra lateral ou botão azul) define onde o consolidado fica: uma planilha que já existe
  ou um nome novo. O app lembra.
- O botão azul mostra **Gravar N novos**: só entram os itens (Sales Doc. + Item) que ainda não estão na
  planilha. Item já gravado não duplica e não muda. Com tudo gravado, o botão diz **Planilha em dia**.
- Antes de gravar, para conferir: o filtro **Todas / Novas / Já gravadas** em cima da tabela, o card
  **Novos para gravar** e o **+N** ao lado de cada arquivo.
- O app grava na própria planilha que você escolheu. Antes de cada gravação ela é copiada para `<nome>.bak.xlsx`,
  para voltar atrás se algo der errado. Para gravar direto, sem criar nenhum outro arquivo, desligue
  **Ajustes > Gravação na planilha > Guardar uma cópia .bak antes de gravar**.
- O app escreve só na aba **Consolidado**. Outras abas (dashboard, tabelas dinâmicas) e colunas escritas à mão
  ficam como estão. Coluna nova da lista entra no fim.
- **Regravar este arquivo na planilha…** (com um arquivo selecionado) troca os valores dos itens dele que já
  estão na planilha pelos de agora, depois de confirmar.
- Com a planilha aberta no Excel, o Windows não deixa gravar: o app avisa para fechar.
- A planilha e a cópia `.bak` são ignoradas se estiverem dentro da pasta DadosCBA.

### Uma planilha que já existe (feita à mão ou pela consulta antiga)

- O app procura os dados em qualquer aba (a `Consolidado` primeiro) e nas primeiras 20 linhas: a linha com
  Sales Doc. e Item é o cabeçalho. O card da planilha diz quantos itens já estão nela e em que aba, e o card
  **Novos para gravar** diz quantos dos arquivos marcados já estão gravados.
- Os nomes das colunas são comparados sem diferenciar maiúsculas, espaços e pontuação (`Sales Doc` =
  `Sales Doc.` = `SALES_DOC`), e alguns nomes alternativos valem para as colunas de base (`Sales Document`,
  `Documento de vendas`, `Criado em`, `Arquivo`…). O pedido `0005940235` é o mesmo que `5940235`.
- Se a planilha estiver organizada diferente do app, o card mostra **Organizada diferente do app** e
  **Ver diferenças e organizar…** lista o que muda: cabeçalho na linha 1 (as linhas de título acima saem), as
  colunas do app com os nomes do app e na ordem do app, as que faltam entram vazias, e as que o app não conhece
  (como uma coluna Obs) ficam depois das do app, com os dados. Pedido, item e quantidade viram números, e
  Created On vira data. Uma Tabela do Excel na aba é refeita com o mesmo nome. Antes, a cópia `.bak.xlsx`
  (se estiver ligada nos Ajustes).
- **Incluir características fora da lista** também vale para organizar. Ligado, as características dos arquivos que
  não estão na lista entram na planilha como colunas. Desligado, as que já estiverem lá (gravadas quando estava
  ligado) contam como diferença, e organizar tira essas colunas, com os dados delas, deixando só as da lista de
  Ajustes. A confirmação diz quantas saem. Colunas escritas à mão, como uma Obs, não são características dos arquivos
  e ficam.
- Uma Tabela do Excel que começa no cabeçalho cresce junto quando o app grava linhas novas.

### Escolher quais arquivos entram

Na barra lateral, a caixinha de cada arquivo (ou Ctrl+clique, ou Shift+clique para um intervalo) marca mais de
um; clicar no nome marca só ele; **Todos os arquivos** marca todos. A tabela, os cards, os avisos, o
**Gravar N novos** e o **Regravar** valem para os arquivos marcados.

## Se der problema

Se o app fechar sozinho, o motivo fica em `%LOCALAPPDATA%\ConsolidadorCBA\erro.log`. Se a janela de escolher
pasta não abrir, cole o caminho da pasta no campo **Ou cole o caminho da pasta** da primeira tela.

## Velocidade

Um mês do SAP (8 MB, 170 mil linhas na aba Data) é lido em menos de 1 s, e até 4 arquivos ao mesmo tempo. A janela
não trava enquanto lê: cada arquivo entra na tabela assim que fica pronto. Com 12 meses desse tamanho: uns 5 s para
abrir a pasta (antes, 1 min 40 s, com a janela parada a maior parte do tempo) e meio segundo para um ajuste. Os
arquivos ficam na memória enquanto o app está aberto; o F5 só lê de novo os que mudaram.

## O que o app faz a mais que a consulta

- Um arquivo com problema (sem aba Data, corrompido, com senha, aberto por outra pessoa) não derruba os outros:
  aparece em vermelho na barra lateral, com o motivo.
- Arquivos temporários `~$...` são ignorados.
- O pivot agrupa só pelas colunas do resultado (Sales Doc., Item, Created On, SOrg., Material, Order Quantity),
  então colunas extras na aba Data não quebram as linhas.
- "Created On" aceita data do Excel ou texto `dd/mm/aaaa`, `dd.mm.aaaa` e `aaaa-mm-dd`.
- Características fora da lista de colunas aparecem como aviso e entram com um clique.
- Busca, ordenação por coluna e seleção de um arquivo só, na barra lateral.

## Como é feito

- `server.go`: o programa. Serve a tela só para a própria janela (127.0.0.1, com verificação do Host e um token),
  lê a pasta, entrega cada arquivo à tela e grava o resultado. Lembra a pasta e os ajustes em
  `%APPDATA%\ConsolidadorCBA\config.json`.
- `sheet.go`: a leitura rápida da aba Data. O `.xlsx` é um zip com XML; o XML da aba é lido uma vez, em sequência, e
  só as colunas que a consolidação usa vão para a tela, numa tabela compacta (cada texto diferente uma vez). Lê também
  o que a planilha consolidada já tem.
- `export.go`: a cópia em Excel, escrita em fluxo (o tamanho não pesa, nem com 1.700 colunas).
- `main_windows.go`: a janela (WebView2, o mesmo componente do Teams e do Outlook novos). Sem o WebView2,
  abre a tela numa janela do Edge.
- `dialogs_windows.go`: as janelas "Escolher pasta" e "Salvar como" do Windows (IFileOpenDialog e
  IFileSaveDialog), chamadas direto.
- `install_windows.go`: `--instalar` e `--desinstalar`.
- `planilha.go`: a planilha consolidada (acrescentar os novos, regravar, organizar), com `excelize`.
- `ui/`: a tela. `core.js` tem a transformação (a mesma lógica da consulta) e `app.js` a interface, organizada
  como um app SwiftUI: view model observável, views pequenas, barra lateral + detalhe, sheet de ajustes e tabela
  que só desenha as linhas visíveis. `xlsx.full.min.js` é a SheetJS 0.18.5 (Apache-2.0, ver `LICENSE-sheetjs.txt`),
  usada só pelo exemplo.
- `main_other.go`: em Linux/Mac serve a mesma tela no navegador, para desenvolvimento e testes
  (`go run . -folder <pasta> -out <pasta de saída>`).

## Gerar o .exe

```sh
go install github.com/tc-hib/go-winres@v0.3.3
./build.sh          # gera dist/ConsolidadorCBA.exe
go test ./...
```
