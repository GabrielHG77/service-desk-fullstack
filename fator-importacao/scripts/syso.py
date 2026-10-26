#!/usr/bin/env python3
"""Embute icone.ico no executável do Windows.

Gera rsrc_windows_amd64.syso, um objeto COFF que o linker do Go junta
automaticamente ao binário de windows/amd64 (o sufixo no nome é o que limita o
arquivo a esse alvo). Escrito à mão para não depender de ferramenta externa;
go-winres e rsrc fazem o mesmo.

    python3 scripts/syso.py

Referência: especificação PE/COFF, seção 5 (.rsrc) e seção 4 (relocations).
"""
import struct
import sys
from pathlib import Path

RT_ICON, RT_GROUP_ICON = 3, 14
RAIZ = Path(__file__).resolve().parent.parent


def ler_ico(caminho):
    b = caminho.read_bytes()
    reservado, tipo, n = struct.unpack_from("<HHH", b, 0)
    if reservado != 0 or tipo != 1 or n == 0:
        sys.exit(f"{caminho} não é um .ico válido")
    imagens, entradas = [], []
    for i in range(n):
        larg, alt, cores, res, planos, bits, tam, desloc = struct.unpack_from("<BBBBHHII", b, 6 + i * 16)
        imagens.append(b[desloc:desloc + tam])
        # No recurso o último campo é o id de 2 bytes, não o deslocamento de 4.
        entradas.append(struct.pack("<BBBBHHIH", larg, alt, cores, res, planos, bits, tam, i + 1))
    grupo = struct.pack("<HHH", 0, 1, n) + b"".join(entradas)
    return imagens, grupo


def diretorio(entradas):
    """IMAGE_RESOURCE_DIRECTORY com entradas só numéricas, que o Windows exige ordenadas."""
    cab = struct.pack("<IIHHHH", 0, 0, 0, 0, 0, len(entradas))
    return cab + b"".join(struct.pack("<II", id_, off) for id_, off in sorted(entradas))


def construir(imagens, grupo):
    recursos = [(RT_ICON, i + 1, img) for i, img in enumerate(imagens)] + [(RT_GROUP_ICON, 1, grupo)]
    tipos = sorted({t for t, _, _ in recursos})
    por_tipo = {t: [r for r in recursos if r[0] == t] for t in tipos}

    # Três níveis: tipo -> nome -> idioma. Os tamanhos são fixos, então dá para
    # calcular todos os deslocamentos antes de escrever qualquer byte.
    off = 16 + 8 * len(tipos)
    off_nome = {}
    for t in tipos:
        off_nome[t] = off
        off += 16 + 8 * len(por_tipo[t])
    off_idioma, off_entrada = {}, {}
    for t in tipos:
        for _, nome, _ in por_tipo[t]:
            off_idioma[(t, nome)] = off
            off += 16 + 8
    for t in tipos:
        for _, nome, _ in por_tipo[t]:
            off_entrada[(t, nome)] = off
            off += 16
    inicio_dados = (off + 7) & ~7

    partes = [diretorio([(t, 0x80000000 | off_nome[t]) for t in tipos])]
    for t in tipos:
        partes.append(diretorio([(nome, 0x80000000 | off_idioma[(t, nome)]) for _, nome, _ in por_tipo[t]]))
    for t in tipos:
        for _, nome, _ in por_tipo[t]:
            partes.append(diretorio([(0x0409, off_entrada[(t, nome)])]))  # en-US

    dados, pos, relocacoes = [], inicio_dados, []
    for t in tipos:
        for _, nome, conteudo in por_tipo[t]:
            relocacoes.append(off_entrada[(t, nome)])
            partes.append(struct.pack("<IIII", pos, len(conteudo), 0, 0))
            dados.append(conteudo)
            pos += (len(conteudo) + 7) & ~7

    rsrc = b"".join(partes)
    rsrc += b"\0" * (inicio_dados - len(rsrc))
    for d in dados:
        rsrc += d + b"\0" * (((len(d) + 7) & ~7) - len(d))
    return rsrc, relocacoes


def escrever(rsrc, relocacoes, saida):
    IMAGE_FILE_MACHINE_AMD64 = 0x8664
    # CNT_INITIALIZED_DATA | ALIGN_8BYTES | MEM_READ
    CARACTERISTICAS = 0x00000040 | 0x00400000 | 0x40000000
    IMAGE_REL_AMD64_ADDR32NB = 0x0003

    off_rsrc = 20 + 40
    off_reloc = off_rsrc + len(rsrc)
    off_simbolos = off_reloc + 10 * len(relocacoes)

    saida.write(struct.pack("<HHIIIHH", IMAGE_FILE_MACHINE_AMD64, 1, 0, off_simbolos, 1, 0, 0))
    saida.write(struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0,
                            len(rsrc), off_rsrc, off_reloc, 0, len(relocacoes), 0, CARACTERISTICAS))
    saida.write(rsrc)
    # Cada IMAGE_RESOURCE_DATA_ENTRY.OffsetToData é um RVA: o linker o corrige
    # quando decide onde a seção cai no executável final.
    for r in relocacoes:
        saida.write(struct.pack("<IIH", r, 0, IMAGE_REL_AMD64_ADDR32NB))
    saida.write(struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0))  # IMAGE_SYM_CLASS_STATIC
    saida.write(struct.pack("<I", 4))  # tabela de nomes vazia


def main():
    ico = RAIZ / "icone.ico"
    if not ico.exists():
        sys.exit(f"{ico} não existe; gere o ícone primeiro (veja o README)")
    imagens, grupo = ler_ico(ico)
    rsrc, relocacoes = construir(imagens, grupo)
    destino = RAIZ / "rsrc_windows_amd64.syso"
    with destino.open("wb") as f:
        escrever(rsrc, relocacoes, f)
    print(f"{destino.name}: {len(imagens)} tamanhos de ícone, {destino.stat().st_size} bytes")


if __name__ == "__main__":
    main()
