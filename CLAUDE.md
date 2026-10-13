# Next TS — resumo completo do projeto (para continuar no Claude Code)

> Junto com este resumo vai o arquivo **Next-TS-1.4.3.zip** (última versão). Dentro dele, a pasta `Next-TS/codigo-fonte` tem todo o código, os testes e as ferramentas de build.

---

## 1. Quem sou eu e como quero trabalhar

- **Sou leigo em programação.** Nunca desenvolvi software. Preciso de explicações simples, passo a passo, sem termos técnicos sem explicar.
- Jogo **Tibia** numa guild que usa **TeamSpeak 3 (TS3)**. A guild usa o **x3tBot** para controlar os respawns (claimed).
- **Não comece a trabalhar antes de eu falar "pode começar".** Antes, tire todas as dúvidas comigo e me mostre o plano.
- Quando terminar: **revise tudo, teste com calma** e me entregue o arquivo pronto (ZIP com o `.exe`), mais uma **prévia** (prints da janelinha).
- Quero um app **pronto para usar**, com janelinha, sem eu mexer em código.
- **Sem som** de aviso.
- Antes eu tentei fazer com o ChatGPT (um app chamado "Chora Dark"), que não funcionou.
- O projeto faz parte do meu **TCC**.
- A automação é permitida na minha guild. Não quero que os outros membros saibam que eu uso, para manter a vantagem.

---

## 2. O que o app faz (ideia geral)

O **Next TS** fica de olho na lista de claimeds que o x3tBot publica na **descrição de um canal** do TeamSpeak (canal "Claimeds"). Quando o respawn que eu escolhi fica na situação certa, ele manda **`!next CÓDIGO`** (ex.: `!next C1`) **no privado do x3tBot**. Manda **uma única vez** e para.

- Ele usa o próprio TeamSpeak (pelo plugin **ClientQuery**) para ler a lista e mandar a mensagem.
- Não mexe em teclado, mouse nem no Tibia. O TS pode ficar minimizado.
- **A versão 1.0.1 (07/10/2026) foi a primeira versão funcional**, confirmada por mim no TeamSpeak real.
- A **1.3.0 (09/10/2026)** funcionou perfeitamente no TS real (confirmado por mim em 13/10/2026).
- A **1.4.0 (13/10/2026)** trouxe "esperar o next desistir" e gravação em vídeo.
- A **1.4.1 (13/10/2026)** corrigiu a leitura do formato novo do x3tBot de 13/10 (tempo com espaços `[ 01:45 ]` e o estado `Aceitar`).
- A **1.4.2 (13/10/2026)** trouxe a regra nova "pega quem já está caçando" e a gravação mais robusta. **Funcionou perfeitamente no TS real** (confirmado por mim em 13/10, com vídeo: `2026-10-13_20-00-12_D13_teste.avi`, janela do TS 2576x1408).
- A versão atual é a **1.4.3 (13/10/2026)**: o vídeo mostra **só a janela do TS** (sem a faixa preta de baixo e sem bordas) e fica bem menor (o aviso rolando na barra do TS deixava ~6-7 GB/h). Testada no TS simulado, no Wine e com as imagens do meu vídeo real; ainda não testei no TS real.

---

## 3. Formato da lista do x3tBot (MUITO IMPORTANTE)

Cada respawn ocupado aparece numa linha. O número entre colchetes no começo, `[HH:MM]`, é **o tempo que FALTA para quem está caçando sair**.

### Formato antigo (ainda pode aparecer)
```
c1 - [00:58] Livraria de Fire (Tier 3): Lord Tenchi | Next: Vexlux +1
```
- Antes de `| Next:` = quem está caçando.
- Sem "Next" = 0 no next. `Next: X` = 1. `Next: X +1` = 2. **O +1 é o máximo.**
- A **"/"** junta nomes da mesma vaga (ex.: `Next: Dudu / Pedrinho`) e **NÃO soma pessoa**.
- As cores (BBCode) não interferem.

### Formato novo (prints de 08 e 09/10/2026)
```
D7 - [00:15] DT -1 (Tier 1): Gnorge Talento Ruro | Next: bruneo berserker/emma Reilly [Espera ~00:15]
R9 - [01:39] Upper Roshamuul (Tier 1): Nowlolah Wolentah | Next: Shawn Shooter [Espera ~01:39] | Next: Billy Noval [Espera ~04:09]
V7 - [00:04] Bulltaur Lair -2 (Tier 1): Red Vesgo | Next: Hardyz Ek [Espera ~00:04]
V7 - [01:14] Bulltaur Lair -2 (Tier 1): Hardyz Ek | Next: Red Vesgo [Fim em ~03:14]
B3 - [01:16] Gloom Pillars: Arthy Zephyr
```
- Códigos em **maiúsculas** (A1, AB1, D13, F5, Y10...).
- **Cada pessoa da fila vem num `| Next:` separado** (no R9 acima tem 2 no next).
- Depois do nome do next pode vir uma etiqueta de tempo: `[Espera ~HH:MM]` ou `[Fim em ~HH:MM]`. Ela **não conta como pessoa**.
  - "Fim em" aparece quando já tem next, para o 2º next saber em quanto tempo entraria na hunt.
- Alguns respawns vêm sem "(Tier N)".
- A "/" continua juntando nomes da mesma vaga.
- **Respawn livre** = o código não aparece na lista.
- Existe também um claim de teste, "Claim MissClick Bot Test" (bot "MissClick Bot Test"), que usa o mesmo formato. O x3tBot teve problemas e às vezes usamos esse.

### Formato de 13/10/2026 (print do Claimed MISSCLICK BOT TEST)
```
C1 - [ 01:55 ] Livraria de Fire (Tier 3): Lord Tenchi | Next: Real Calvon [ Fim em ~03:55 ]
R1 - [ 00:26 ] West (Tier 1): Rhoten | Next: Apoloooo [ Fim em ~02:26 ] | Next: Megazord
R9 - [ Aceitar 00:03 ] Upper Roshamuul (Tier 1): Next: Alxoff — aguardando aceitação
```
- O tempo agora vem **com espaços dentro dos colchetes**: `[ 01:55 ]`. As versões até a 1.4.0 não liam isso (bug de 13/10: com a janela ligada nunca enviava; com ela desligada mandava o next de quem já estava caçando ~1 min depois do Iniciar). Corrigido na 1.4.1.
- `[ Aceitar 00:03 ]`: quem caçava saiu e o 1º da fila está aceitando o respawn. **Ninguém está caçando ainda**: não é entrada nem falha. O nome vem com `— aguardando aceitação`, que não faz parte do nome. Quando a pessoa aceita, aparece como caçador normal (`[ 02:00 ] ...: Alxoff`).
- Outra palavra desconhecida no lugar do tempo: o app não envia nada e avisa no Histórico.

---

## 4. Regras atuais do app (versão 1.4.3, combinadas comigo)

1. **Só 1º next.** Eu não posso mais pegar 2º next. A opção de 2º next e a opção "pegar logo se já estiver disponível" foram removidas.
2. **Quem já está caçando ao Iniciar (mudou na 1.4.2, "sempre pegar"):** se já tiver alguém caçando e **ninguém no next**, e a conta der **dentro da janela** (ou a janela estiver desligada), o app pega **na hora** (atraso de 1 a 2 s + conferência). Exemplo do dia 13/10: 19:26 + `[ 00:35 ]` = 20:01, dentro de 20:00–22:30.
   - Se der **fora da janela**, se **já tiver alguém no next**, ou se a linha não mostrar o tempo (com a janela ligada): **não falha** o plano, só espera a próxima pessoa entrar. Com "esperar o next desistir" ligado, espera o next dessa hunt desistir (e, se esse next entrar na hunt, conta como entrada normal, sem falhar).
   - Se pegarem o next dessa hunt durante o atraso de 1 a 2 s: também não falha, espera a próxima.
   - Depois disso, o app age quando **ALGUÉM ENTRA na hunt** (regras de sempre).
   - Até a 1.4.1 a regra era o contrário: "quem já estava lá antes ele não pega".
3. **Respawn livre: nunca envia nada.**
4. **Janela de início** (opcional, caixinha "Monitorar janela de início"). Eu escolho o horário, ex.: das 20:00 às 22:30.
   - Quando alguém entra na hunt: **hora atual (relógio do Windows) + tempo que falta** = hora em que minha hunt começaria.
     Exemplo: 18:30 + `[01:45]` = 20:15. Está dentro da janela, então pega.
   - Fora da janela (antes ou depois): **não pega**, e o plano falha.
   - A conta é minuto a minuto e as pontas contam como dentro: 20:00 e 22:30 pegam, 22:31 não. Pode haver 1 minuto de diferença, porque o x3tBot mostra só minutos.
   - Funciona passando da meia-noite (ex.: 23:00 às 01:00).
   - **Passou do fim da janela, o app para sozinho** ("Janela encerrada").
   - Se a janela de hoje já acabou quando eu clico em Iniciar, ele avisa e não começa.
   - Com a janela ligada, se a linha não mostrar o tempo, ele não envia (não tem como conferir).
5. **Plano A e Plano B** (o B é opcional; a janela é a mesma para os dois).
   - O Plano A **falha** quando alguém entra na hunt e já tem next (pegaram antes de mim), quando alguém pega o next durante a espera, ou quando o horário fica fora da janela.
   - A falhou: **passa para o B e NUNCA volta para o A.**
   - O B segue as mesmas regras. Na troca, quem **já está** caçando no B sem next (dentro da janela) é pego na hora, igual à regra 2.
   - B falhou: **o app para.**
   - Se A e B ficassem bons ao mesmo tempo, vale o A.
   - **Pega um next só, NUNCA dois.**
6. Respawn ficou livre durante a espera: não é falha, continua esperando.
7. Se a linha "piscar" (sumir e voltar com a mesma pessoa), isso não conta como alguém entrando. A mesma pessoa com o tempo pulando para cima (fez claim de novo) conta como hunt nova.
8. **Lê a lista a cada 0,5 s.**
9. Quando dá certo, espera um **atraso aleatório de 1 a 2 s** (antes era de 1,8 a 2,8 s), **confere a lista de novo** e só então envia.
10. **Modo teste:** faz tudo, mas não envia (mostra "eu teria enviado...").
11. "Parar" cancela tudo, inclusive um envio que estava para sair. Se o comando já estava saindo, ele mostra o resultado certo.
12. Se o TS cair ou desconectar do servidor: não envia nada e volta sozinho quando o TS voltar.
13. **Esperar o next desistir** (opcional, caixinha; desligada = igual à 1.3.0):
    - Alguém entra na hunt **já com next**: não falha na hora. Espera o next **sair da fila** (next vazio) e então manda (atraso 1 a 2 s + conferência).
    - 2 no next e só 1 desiste: continua esperando.
    - O next **entra na hunt** em vez de desistir (comparado pelo nome; a "/" vale qualquer um dos nomes): o plano **falha**. Também falha se a lista mostrar um instante sem o next e logo depois essa pessoa caçando.
    - Alguém pega o next durante o atraso de 1 a 2 s: cancela o envio e volta a esperar esse next desistir.
    - Janela ligada: se a hunt já começaria fora da janela, falha na hora (nem espera). Na desistência, a conta é refeita.
    - Respawn ficou livre na espera: não é falha. Se entrar **outra** pessoa, é hunt nova (regras normais).
    - Hunt que já existia ao Iniciar, com next: espera esse next desistir e pega (regra 2). Se o next entrar na hunt, não falha: vira entrada normal.
14. **Gravação em vídeo** (opcional, caixinha):
    - Grava **só a janela do TeamSpeak**, do tamanho exato dela, **sem faixa nem bordas** (pedido em 13/10: "apenas a janela do TS e só"), **5 quadros/s**, do Iniciar até **10 s depois de enviar** (ou do "teria enviado" no Modo teste). Parar/falha/janela encerrada: termina 2 s depois.
    - Até a 1.4.2 havia uma faixa preta embaixo com relógio, "REC" e acontecimentos: **foi tirada** na 1.4.3. O horário e o "ENVIADO" ficam no Histórico e no `registro.txt`.
    - Partes que **se mexem sozinhas** o tempo todo (ex.: o aviso "Announcement" que rola na barra de baixo do TS) não fazem gravar imagem nova: aparecem atualizadas **a cada 2 s**. Qualquer outra mudança (chat, lista) entra na hora, a 5 quadros/s. (Sem isso, o vídeo real dava ~6-7 GB/h; com isso, ~0,6-0,7 GB/h.)
    - TS minimizado/fechado: o vídeo mostra uma **tela com o aviso** no lugar da janela (eu escolhi manter assim).
    - TS mudou de tamanho no meio: a imagem é ajustada ao tamanho do começo.
    - O "!next" aparece no chat do TS porque sai do próprio TS (ClientQuery). Eu preciso deixar **aberto e selecionado o chat privado com o bot** e **não minimizar** o TS (atrás do Tibia pode). Minimizado: o vídeo mostra um aviso no lugar da janela.
    - Pasta `%LOCALAPPDATA%\NextTS\gravacoes`, nome `AAAA-MM-DD_HH-MM-SS_C1.avi` (`_teste` no Modo teste). **Nunca apaga vídeos antigos.** Acima de 1 GB continua em `_parte2`.
    - Quadro igual ao anterior não é gravado de novo (vídeo menor). Formato AVI/MJPEG: abre no VLC.
    - Problema na gravação nunca atrapalha o monitoramento nem o envio.
    - Envio "por escrito" (digitar no TS) em vez do ClientQuery: eu disse **"deixa pra depois"**.

---

## 5. Como o app foi feito (parte técnica)

- **Linguagem:** Go 1.24, só a biblioteca padrão. Gera um único `Next TS.exe` para Windows (compilado com `-H windowsgui`, sem janela preta).
- **Janelinha:** página HTML local (`web/index.html`, embutida no exe) servida em `127.0.0.1` numa porta aleatória, com código de acesso (token). Ela abre no Edge ou Chrome em modo app, com um perfil separado. Fechar a janelinha fecha o app.
- **TeamSpeak:** ClientQuery em `127.0.0.1:25639`, autenticado com a **API Key** (TS → Ferramentas → Opções → Addons → ClientQuery → Configurações).
  - Comandos usados: `auth`, `use`, `whoami`, `serverconnectionhandlerlist`, `servervariable`, `clientvariable`, `channellist`, `clientlist -uid`, `channelvariable cid=X channel_description`, `sendtextmessage targetmode=1`, `clientnotifyregister` (para ver a resposta do bot).
  - O bot é achado pela **UID**, porque o número dele muda quando ele reconecta.
- **Chave:** salva protegida pelo Windows (DPAPI). Dados em `%LOCALAPPDATA%\NextTS`: `preferencias.json`, `registro.txt` (log sem a chave), `instancia.json` e a pasta `gravacoes`.
- **Gravação:** acha a janela do TS pelo programa (`ts3client_win64.exe`/`ts3client_win32.exe`): visível, sem dona, não é "janela de ferramenta", não está escondida (cloaked), pelo menos 200x150; a maior. Copia a imagem tentando 3 jeitos (lembra o que funcionou): `PrintWindow(PW_RENDERFULLCONTENT)`/`PrintWindow` (pega coberta), `GetWindowDC`+`BitBlt`, e direto da tela (só se a janela estiver visível na frente: 3 de 5 pontos com `WindowFromPoint`). Usa `CreateDIBSection`. Anota no `registro.txt` ("[gravação] ...") a janela escolhida, o jeito usado e os motivos das falhas. Detecta TS como administrador e Next TS não (aviso para abrir o Next TS como administrador). O app se declara "DPI aware" para pegar o tamanho real da janela. Grava AVI com imagens JPEG (biblioteca padrão do Go); quadros repetidos viram "pedaços vazios" (o tempo do vídeo continua certo). Decide se grava imagem nova comparando em quadradinhos de 32x32 (`frameDiff`): quadradinho que mudou em 6 das últimas 10 capturas "se mexe sozinho", e os vizinhos dele na mesma linha (até 4 para cada lado) também, porque texto rolando sempre entra em quadradinho novo pela ponta. Os avisos no lugar da janela usam letras da fonte DejaVu Sans Mono embutidas em `fonte_dados.go`.
- **Uma cópia só:** se abrir de novo, a cópia nova fecha (ou fecha a versão antiga, se for mais nova).
- **Conecta sozinho:** se o TS abrir depois, o app conecta quando o TS estiver no servidor. Se a chave for recusada, para de tentar até eu colar outra.
- **Escolha do canal:** prefere o que eu escolhi; depois, um canal que tenha a lista de verdade; depois, o nome "Claimeds". Canais de teste/erro/regras perdem pontos. Se o canal estiver sem lista, o app não deixa iniciar (já aconteceu de ele escolher "Claimed TEST", que estava vazio).
- **Atalhos de claim:** botões que trocam canal e bot de uma vez (x3tBot → canal "Claimeds"; MissClick Bot Test → "Claim MissClick Bot Test"). Posso salvar e apagar atalhos.
- **Lista de respawns:** 116 respawns agrupados por cidade (`catalog.go`), mais a opção "Outro código (digitar)".
- **Visual:** tema medieval/Tibia, desenhado no próprio app (pedra, dourado, pergaminho). Título "NEXT TS" em pixel, tocha, céu estrelado à noite e um **guerreiro de armadura de ferro (desenho próprio)** que chora enquanto espera e comemora quando pega. Não usa arte do jogo.
- A janelinha mostra o **relógio do PC** que o app usa, a **conta do horário** feita e os planos (o A aparece riscado quando passa para o B).

### Arquivos do código (`codigo-fonte/`)
| Arquivo | O que faz |
|---|---|
| `main.go` | Servidor local, rotas `/api/...`, abre o navegador, uma cópia só, `initClock()` |
| `app.go` | Motor: conexão, leitura a cada 0,5 s, Plano A/B, janela, atraso, envio, estado da janela |
| `queue.go` | Lê a lista do x3tBot (formatos novo e antigo, tempo que falta, contagem do next) |
| `record.go` | Gravação: arquivo AVI/MJPEG, só a janela do TS, imagem nova ou repetida (`frameDiff`), início/fim |
| `capture_windows.go` | Acha a janela do TS e copia a imagem dela (Windows) |
| `capture_other.go` | Janela do TS de mentira para os testes no Linux (`NEXTTS_FAKE_TS`) |
| `fonte_dados.go` | Letras dos avisos do vídeo (gerado por `build/gerar_fonte.py`) |
| `window.go` | Janela de horário (`ParseHM`, `ResolveWindow`) e relógio (`now()`) |
| `query.go` | Cliente do ClientQuery do TS3 |
| `config.go` | Versão (`1.4.3`), preferências salvas, log em arquivo |
| `presets.go` | Atalhos de claim |
| `catalog.go` | Lista dos respawns |
| `platform_windows.go` / `platform_other.go` | DPAPI, abrir o navegador, caixas de mensagem |
| `web/index.html` | A janelinha inteira (HTML, CSS e JS num arquivo só) |
| `queue_test.go` | Testes da leitura da lista e da janela (`go test`) |
| `record_test.go` | Testes do gravador e da comparação de nomes (`go test`) |
| `capture_windows_test.go` | Teste da captura de verdade no Windows/Wine (`NEXTTS_WINE_TEST=1`, `desktop` ou `outro`; executável chamado `ts3client_...`) |
| `testes/mock_ts3.py` | TeamSpeak simulado (ClientQuery falso com x3tBot e MissClick Bot Test) |
| `testes/cenarios.py` | 77 cenários de ponta a ponta com o app de verdade (usa `ffprobe`/`ffmpeg` para conferir os vídeos) |
| `testes/interface.py` | Testa a janelinha num Chromium (Playwright) e tira prints |
| `build/gerar_recursos.py` | Gera ícone e informações de versão do .exe (`.syso`) |
| `build/gerar_fonte.py` | Gera `fonte_dados.go` (letras da faixa do vídeo) |
| `build/conferir_exe.py` | Confere o .exe gerado |
| `build/LEIA-ME.txt` | Manual para mim (vai no ZIP) |

### Como compilar e testar
```bash
python3 build/gerar_fonte.py .
python3 build/gerar_recursos.py . 1.4.3
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o "Next TS.exe" .
python3 build/conferir_exe.py "Next TS.exe"
go test ./...
go build -race -o nextts-linux . && python3 -I testes/cenarios.py ./nextts-linux     # 77/77
python3 -I testes/interface.py ./nextts-linux prints/                              # INTERFACE OK
```
Variáveis só para testes: `NEXTTS_DATA` (pasta de dados), `NEXTTS_NO_BROWSER=1`, `NEXTTS_AUTO_EVERY`, `NEXTTS_TEST_CLOCK=HH:MM:SS` (simula o relógio), `NEXTTS_FAKE_TS=<arquivo>` (janela do TS de mentira no Linux: texto, `min` ou `none`; linha `ROLANDO: ...` vira um aviso rolando) e `NEXTTS_PRINTS=<pasta>` (salva imagens dos vídeos dos cenários).

O ZIP entregue tem: `Next-TS/Next TS.exe`, `Next-TS/LEIA-ME.txt` e `Next-TS/codigo-fonte/`.

---

## 6. Histórico de versões

- **1.0.0:** primeira entrega. Bug: escolheu o canal vazio "Claimed TEST".
- **1.0.1 (07/10): PRIMEIRA VERSÃO FUNCIONAL** no TS real. Escolhe o canal pela lista de verdade, mostra a lista completa de respawns.
- **1.1.0 (07/10):** tema medieval com o guerreiro. Regra da fila: "+1" soma, "/" não soma.
- **1.2.0 (08/10):** atalhos de claim (x3tBot / MissClick), escolha lembrada, conecta sozinho, avisos de erro que ficam na tela, tocha, céu e guerreiro animado.
- **1.2.1 (08/10):** correção do Parar bem na hora do envio; aba certa do TS antes de enviar; visual refinado.
- **1.3.0 (09/10):** só 1º next; age só quando alguém entra; janela de início; Plano A/B; atraso de 1 a 2 s; formato novo do x3tBot; relógio e conta na tela; proteção contra a linha "piscar". 48 cenários aprovados. **Funcionou perfeitamente no TS real.**
- **1.4.0 (13/10):** caixinha "Esperar o next desistir"; gravação em vídeo da janela do TS (5 quadros/s, faixa com relógio, até 10 s depois de enviar). 64 cenários aprovados; captura do Windows testada no Wine.
- **1.4.1 (13/10):** correção do formato novo do x3tBot: tempo com espaços `[ 01:45 ]` (na 1.3.0 o app não pegou o next do C1 com a janela ligada: "não consegui ler o tempo") e estado `[ Aceitar ... ]` (não é entrada nem falha). 70 cenários aprovados.
- **1.4.2 (13/10):** regra 2 nova: pega quem **já está** caçando sem next ao Iniciar (e na troca para o B), se a conta der dentro da janela; fora da janela ou com next, não falha e espera a próxima. Gravação: escolhe melhor a janela do TS, tenta 3 jeitos de copiar, anota tudo no `registro.txt`, avisa se o TS estiver como administrador. 76 cenários aprovados; captura testada no Wine também com o TS como outro programa. **Funcionou perfeitamente no TS real.**
- **1.4.3 (13/10):** vídeo só com a janela do TS (sem faixa e sem bordas); o que se mexe sozinho (aviso rolando) atualiza a cada 2 s, o resto na hora (vídeo de ~6-7 GB/h para ~0,6-0,7 GB/h numa tela 2576x1408). 77 cenários aprovados. Pendente: uma tarja preta aparece sobre a barra de título do TS no vídeo real (canto de cima à esquerda); falta o `registro.txt` para saber qual jeito de copiar foi usado.

---

## 7. Ideias pendentes (NÃO fazer sem eu pedir)

- **Modo "só Enter" / envio "por escrito":** o app digitaria (ou só apertaria Enter) no chat privado do x3tBot em vez de mandar pelo ClientQuery. Eu disse "deixa pra depois" (de novo em 13/10). Riscos explicados: rouba o foco do Tibia, precisa da aba certa (senão vai para o canal), mexe no texto digitado. Para o bot e os outros membros, os dois jeitos são iguais.
- Ideias que já foram sugeridas: aviso de "formato da lista mudou", botão "Copiar lista", favoritos, atalhos de teclado.
- Achei desnecessário: acompanhar a fila e destacar a resposta do x3tBot.
- Primeira coisa a fazer: **testar a 1.4.3 no TeamSpeak real**, em Modo teste primeiro, com a gravação ligada (conferir se o vídeo mostra a janela do TS e o "!next" no chat).

## 8. Coisas que ficaram de fora na conversa anterior

Prints da tela do TS (a gravação em vídeo foi feita na 1.4.0), diário automático, imagem imitando print do TS, disfarce de "parecer digitado", outfit do Tibia (Golden Outfit) no boneco e acessar o código do x3tBot. Nada disso está no app.
