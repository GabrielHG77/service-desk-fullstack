# Consolidador CBA

App de navegador que faz o mesmo que a consulta do Power Query da pasta `DadosCBA`:
lê a aba **Data** de todos os `.xlsx`/`.xlsm` de uma pasta (com subpastas), transforma
cada característica (`Internal char no.`) em uma coluna e junta tudo em uma tabela,
uma linha por item de ordem de venda.

## Como usar

1. Copie `Consolidador-CBA.html` para o computador e abra com dois cliques (Edge ou Chrome).
   Funciona sem internet: a biblioteca que lê Excel já está dentro do arquivo.
2. Clique em **Escolher pasta** e selecione a pasta `DadosCBA` (ou arraste a pasta para a página).
3. Confira os avisos e clique em **Baixar Excel**.

Os arquivos são lidos só no navegador; nada é enviado para fora do computador.

## Diferenças em relação à consulta original

- Um arquivo com problema (sem aba Data, corrompido, com senha) não derruba os outros: aparece como erro na lista.
- Arquivos temporários `~$...` são ignorados e a extensão não diferencia maiúsculas.
- O pivot agrupa só pelas colunas do resultado (Sales Doc., Item, Created On, SOrg., Material,
  Order Quantity), então colunas extras na aba Data não quebram as linhas.
- "Created On" aceita data do Excel ou texto `dd/mm/aaaa`, `dd.mm.aaaa` e `aaaa-mm-dd`.
- Características fora da lista de colunas são mostradas como aviso e podem ser incluídas com um clique.

## Arquivos

- `Consolidador-CBA.html`: versão para usar, com tudo embutido.
- `app.html`: código-fonte da página (carrega a SheetJS do cdnjs).
- `build_offline.py`: gera a versão embutida:
  `npm install xlsx@0.18.5` e depois
  `python3 build_offline.py app.html node_modules/xlsx/dist/xlsx.full.min.js Consolidador-CBA.html`.
- `LICENSE-sheetjs.txt`: licença da SheetJS (Apache-2.0), biblioteca usada para ler e gravar Excel.
