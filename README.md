# Next TS

App para Windows que vigia a lista de claimeds do **x3tBot** no **TeamSpeak 3** e manda `!next CÓDIGO` no privado do bot quando alguém entra na hunt do respawn escolhido. Envia uma única vez.

**Versão atual: 1.4.0**

## O que ele faz
- Lê a lista do canal **Claimeds** a cada 0,5 s, pelo plugin **ClientQuery** do TeamSpeak.
- Só pega o **1º next**, e só quando **alguém entra na hunt** depois de você clicar em Iniciar.
- **Janela de início** (opcional): hora atual + tempo que falta tem que cair no horário escolhido (ex.: 20:00–22:30).
- **Plano A / Plano B**: se o A falhar, passa para o B e não volta.
- Atraso aleatório de 1 a 2 s, confere de novo e envia.
- Lê o formato antigo e o novo da lista do x3tBot.
- **Esperar o next desistir** (opcional): se alguém entra na hunt já com next, espera o next sair da fila e então envia; se o next entrar na hunt, o plano falha.
- **Gravação em vídeo** (opcional) da janela do TeamSpeak: 5 quadros/s, do Iniciar até 10 s depois de enviar, com faixa de relógio e acontecimentos. Salva em `%LOCALAPPDATA%\NextTS\gravacoes` (AVI/MJPEG, abre no VLC).
- Modo teste (não envia nada).

## Como usar
Veja o [build/LEIA-ME.txt](build/LEIA-ME.txt).

## Como compilar (Go 1.24)
```bash
python3 build/gerar_fonte.py .        # letras da faixa do vídeo (fonte_dados.go)
python3 build/gerar_recursos.py . 1.4.0
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o "Next TS.exe" .
python3 build/conferir_exe.py "Next TS.exe"
```

## Testes
```bash
go test ./...
go build -race -o nextts-linux .
python3 -I testes/cenarios.py ./nextts-linux        # 64 cenários com TeamSpeak simulado (precisa do ffmpeg/ffprobe)
python3 -I testes/interface.py ./nextts-linux prints/  # janelinha no Chromium (precisa do Playwright)
```

## Estrutura
| Arquivo | Para quê |
|---|---|
| `main.go` | Servidor local da janelinha e rotas `/api` |
| `app.go` | Motor: conexão, leitura, planos A/B, janela, envio |
| `queue.go` | Leitura da lista do x3tBot |
| `record.go` | Gravação em vídeo (AVI/MJPEG, faixa com relógio) |
| `capture_windows.go` / `capture_other.go` | Imagem da janela do TS (Windows) / janela simulada (testes) |
| `fonte_dados.go` | Letras da faixa do vídeo (gerado por `build/gerar_fonte.py`) |
| `window.go` | Janela de horário e relógio |
| `query.go` | Cliente do ClientQuery do TS3 |
| `config.go` | Versão e preferências |
| `presets.go` | Atalhos de claim |
| `catalog.go` | Lista de respawns |
| `web/index.html` | A janelinha |
| `testes/` | TeamSpeak simulado e testes |
| `build/` | Ícone, recursos do .exe e manual |
| `CLAUDE.md` | Resumo completo do projeto e das regras (para o Claude Code) |
