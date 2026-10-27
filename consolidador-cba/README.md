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
3. Confira os erros e avisos e clique em **Exportar Excel**. Depois de salvar, **Abrir no Excel** abre o arquivo.

Atalhos: F5 atualiza, Ctrl+F busca, Ctrl+E exporta, Ctrl+, abre os Ajustes.

Na primeira vez, o Windows pode mostrar "O Windows protegeu o computador", porque o programa
não é assinado. Clique em **Mais informações** e depois em **Executar assim mesmo**.

## Se der problema

Se o app fechar sozinho, o motivo fica em `%LOCALAPPDATA%\ConsolidadorCBA\erro.log`. Se a janela de escolher
pasta não abrir, cole o caminho da pasta no campo **Ou cole o caminho da pasta** da primeira tela.

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
  lê a pasta, entrega os arquivos à tela e grava o resultado. Lembra a pasta e os ajustes em
  `%APPDATA%\ConsolidadorCBA\config.json`.
- `main_windows.go`: a janela (WebView2, o mesmo componente do Teams e do Outlook novos). Sem o WebView2,
  abre a tela numa janela do Edge.
- `dialogs_windows.go`: as janelas "Escolher pasta" e "Salvar como" do Windows (IFileOpenDialog e
  IFileSaveDialog), chamadas direto.
- `install_windows.go`: `--instalar` e `--desinstalar`.
- `ui/`: a tela. `core.js` tem a transformação (a mesma lógica da consulta) e `app.js` a interface, organizada
  como um app SwiftUI: view model observável, views pequenas, barra lateral + detalhe, sheet de ajustes e tabela
  que só desenha as linhas visíveis. `xlsx.full.min.js` é a SheetJS 0.18.5 (Apache-2.0, ver `LICENSE-sheetjs.txt`).
- `main_other.go`: em Linux/Mac serve a mesma tela no navegador, para desenvolvimento e testes
  (`go run . -folder <pasta> -out <pasta de saída>`).

## Gerar o .exe

```sh
go install github.com/tc-hib/go-winres@v0.3.3
./build.sh          # gera dist/ConsolidadorCBA.exe
go test ./...
```
