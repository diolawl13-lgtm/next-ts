"""Listas de exemplo usadas nos testes."""


def desc(*lines, last="2026-10-06 18:02:13"):
    body = "\n".join(f"[b]{l}[/b]" for l in lines)
    return (f"[center][img]https://x3t.example/claimed.png[/img][/center]\n"
            f"[i]Last update: {last} America/Sao_Paulo[/i]\n\n{body}")


OCUP0 = "c1 - [01:30] Livraria de Fire (Tier 3): [color=blue]Kira Zinha[/color]"
OCUP1 = OCUP0 + " | Next: [color=blue]Lord Calvon[/color]"
OCUP2 = OCUP0 + " | Next: [color=blue]Lord Calvon[/color] +1"
OUTRAS = ["a1 - [00:22] Cobra Castelo (Tier 1): Jairo Slayer | Next: Lord Tenchi",
          "d15 - [00:35] Caminho Ferumbras (Tier 1): Guiixd",
          "c10 - [00:10] Teste: Fulano"]
