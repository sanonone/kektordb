# KektorDB Wire Protocol

KektorDB usa un protocollo testuale semplice, basato su righe di testo terminate da `\r\n` (CRLF). Ogni comando è una serie di stringhe separate da spazi.

### Comandi del Client

I comandi sono inviati come una singola riga.

- `PING`
- `SET <key> <value>`
- `GET <key>`
- `DEL <key>`

### Risposte del Server

Il server risponde con un primo byte che indica il tipo di risposta.

1.  **Simple Strings:** Risposte semplici come `+OK` o `+PONG`. Iniziano con `+`.
    -   Esempio: `+OK\r\n`

2.  **Errors:** Messaggi di errore. Iniziano con `-`.
    -   Esempio: `-ERROR Comando non trovato\r\n`

3.  **Bulk Strings:** Dati di una lunghezza specifica, usati per restituire i valori. Iniziano con `$`, seguito dalla lunghezza in byte del valore, CRLF, il valore stesso, e un altro CRLF.
    -   Esempio per il valore "ciao": `$4\r\n`ciao`\r\n`

4.  **Null Bulk Strings:** Usato quando un valore non esiste.
    -   Esempio: `$-1\r\n`
