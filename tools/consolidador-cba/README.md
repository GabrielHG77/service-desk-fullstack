# Consolidador CBA

App para Windows que faz o mesmo que a consulta do Power Query da pasta `DadosCBA`:
lê a aba **Data** de todos os `.xlsx`/`.xlsm` de uma pasta (com subpastas), transforma
cada característica (`Internal char no.`) em uma coluna e junta tudo em uma tabela,
uma linha por item de ordem de venda.

## Como usar

1. Copie `dist/ConsolidadorCBA.exe` para o computador (por exemplo, na Área de Trabalho) e abra com dois cliques.
   Não precisa instalar nada.
2. Clique em **Escolher pasta…** e selecione `Y:\...\Dashboard\2026\DadosCBA`.
   Da próxima vez o app já abre nessa pasta.
3. Confira os erros e avisos e clique em **Exportar Excel**. Depois de salvar, **Abrir no Excel** abre o arquivo.

Atalhos: F5 atualiza, Ctrl+F busca, Ctrl+E exporta, Ctrl+, abre os Ajustes.

Na primeira vez, o Windows pode mostrar "O Windows protegeu o computador", porque o programa
não é assinado. Clique em **Mais informações** e depois em **Executar assim mesmo**.

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
- `main_windows.go`: a janela (WebView2, o mesmo componente do Teams e do Outlook novos) e as janelas de
  "Escolher pasta" e "Salvar como" do Windows. Sem o WebView2, abre a tela numa janela do Edge.
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
