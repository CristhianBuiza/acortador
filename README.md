# Acortador

Acortador de links autoalojado, escrito en Go, con un front mínimo en HTML, CSS y JavaScript sin frameworks. Todo se compila en **un único binario** (incluye el front y usa SQLite embebido), así que en el servidor no necesitas Go, Node ni una base de datos aparte.

Pensado para montarse en un subdominio propio, por ejemplo `s.tudominio.com`, detrás de Nginx.

## Características

- Links cortos con código aleatorio de 7 caracteres o con **alias personalizado** (`s.tudominio.com/promo`).
- Redirección `302` con **conteo de clics**.
- Panel en la misma página para ver, abrir y borrar tus links.
- Protección por **token de acceso** (`ADMIN_TOKEN`) para que nadie use tu dominio para spam o phishing.
- Límite de 30 intentos por minuto por IP sin token válido, contra la fuerza bruta. Las peticiones autenticadas no se limitan.
- Validación de URLs: solo `http`/`https`, máximo 2048 caracteres y sin bucles hacia el propio acortador.
- Cabeceras de seguridad (CSP, `nosniff`, `no-referrer`, sin iframes).
- Sin rastreadores, sin analítica de terceros y sin cookies.
- Binario estático (sin CGO), de unos 11 MB.

## Requisitos

| Para | Necesitas |
|---|---|
| Compilar | Go 1.26 o superior |
| Ejecutar en el servidor | Linux x86-64 (u otra arquitectura si compilas para ella) |
| Publicarlo en un dominio | Nginx (u otro proxy inverso), un subdominio y un certificado TLS |

## Inicio rápido (en tu máquina)

```bash
git clone https://github.com/CristhianBuiza/acortador.git
cd acortador
ADMIN_TOKEN=mi-token go run .
```

Abre <http://127.0.0.1:8095>. Despliega **"Acceso y mis links"**, pega `mi-token` en el campo del token y pulsa **Guardar**. Desde ahí ya puedes acortar links.

Para ejecutar los tests:

```bash
go test ./...
```

## Configuración

Toda la configuración va en variables de entorno:

| Variable | Por defecto | Descripción |
|---|---|---|
| `ADDR` | `127.0.0.1:8095` | Dirección y puerto donde escucha. Déjalo en `127.0.0.1` si va detrás de Nginx. |
| `DB_PATH` | `acortador.db` | Ruta del archivo SQLite. Se crea solo si no existe. |
| `BASE_URL` | `http://127.0.0.1:8095` | URL pública del acortador, **sin barra final**. Se usa para construir los links cortos y para impedir que se acorte a sí mismo. |
| `ADMIN_TOKEN` | *(vacío)* | Token necesario para crear, listar y borrar links. **Si lo dejas vacío, cualquiera puede hacerlo**: úsalo vacío solo en local. |
| `TRUST_PROXY` | *(vacío)* | Ponlo en `1` cuando esté detrás de Nginx o Cloudflare, para que el límite de peticiones use la IP real del cliente (`CF-Connecting-IP` o `X-Real-IP`). |

Ejemplo de archivo `acortador.env` (está en [`deploy/acortador.env.example`](deploy/acortador.env.example)):

```ini
ADDR=127.0.0.1:8095
DB_PATH=/opt/acortador/data/acortador.db
BASE_URL=https://s.ejemplo.com
ADMIN_TOKEN=cambia-esto-por-un-token-largo
TRUST_PROXY=1
```

Para generar un token seguro:

```bash
openssl rand -hex 24
```

## Desplegarlo en tu propio dominio

En los ejemplos se usa `s.ejemplo.com` y el servidor `203.0.113.10`. Cámbialos por los tuyos.

### 1. Subdominio (DNS)

Crea un registro `A` que apunte el subdominio a la IP de tu servidor:

| Tipo | Nombre | Valor |
|---|---|---|
| A | `s` | `203.0.113.10` |

Si usas **Cloudflare**, puedes activar el proxy (nube naranja). En ese caso usa el modo SSL **Full** o **Full (strict)** y deja `TRUST_PROXY=1`.

### 2. Compilar y subir el binario

**Opción A: script automático.** [`deploy/deploy.sh`](deploy/deploy.sh) ejecuta los tests, compila para Linux, sube el binario por SSH, crea un usuario de sistema sin privilegios, genera `acortador.env` con un `ADMIN_TOKEN` aleatorio (solo la primera vez) e instala y arranca el servicio de systemd.

```bash
HOST=root@203.0.113.10 DOMAIN=s.ejemplo.com ./deploy/deploy.sh

# Opcionales: SSH_PORT (22 por defecto) y APP_PORT (8095 por defecto)
HOST=root@203.0.113.10 SSH_PORT=2222 APP_PORT=9000 DOMAIN=s.ejemplo.com ./deploy/deploy.sh
```

Para ver el token generado:

```bash
ssh root@203.0.113.10 'grep ADMIN_TOKEN /opt/acortador/acortador.env'
```

Para actualizar más adelante, vuelve a ejecutar el mismo comando: reemplaza el binario y reinicia el servicio, pero conserva la base de datos y el `.env`.

**Opción B: a mano.**

```bash
# En tu máquina
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/acortador .
scp dist/acortador deploy/acortador.service root@203.0.113.10:/tmp/

# En el servidor
useradd --system --no-create-home --shell /usr/sbin/nologin acortador
mkdir -p /opt/acortador/data
install -m 755 /tmp/acortador /opt/acortador/acortador
install -m 600 /dev/null /opt/acortador/acortador.env   # y rellénalo como en el ejemplo de arriba
chown -R acortador:acortador /opt/acortador/data
```

> Para un servidor ARM (por ejemplo Raspberry Pi o Ampere), compila con `GOARCH=arm64`.

### 3. Servicio systemd

[`deploy/acortador.service`](deploy/acortador.service) ejecuta el binario como el usuario `acortador`, lee la configuración de `/opt/acortador/acortador.env` y solo puede escribir en `/opt/acortador/data`.

```bash
install -m 644 /tmp/acortador.service /etc/systemd/system/acortador.service
systemctl daemon-reload
systemctl enable --now acortador

systemctl status acortador        # estado
journalctl -u acortador -f        # logs
curl http://127.0.0.1:8095/healthz  # debe responder "ok"
```

### 4. Nginx como proxy inverso

El acortador escucha solo en `127.0.0.1`, y Nginx lo publica en el subdominio con HTTPS. Bloque de servidor completo:

```nginx
server {
    listen 80;
    server_name s.ejemplo.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name s.ejemplo.com;

    ssl_certificate     /etc/letsencrypt/live/s.ejemplo.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/s.ejemplo.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8095;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Para obtener el certificado con Let's Encrypt:

```bash
certbot --nginx -d s.ejemplo.com
nginx -t && systemctl reload nginx
```

**Si usas aaPanel:** crea el sitio `s.ejemplo.com` desde el panel, emite el certificado SSL desde la pestaña SSL y pega el bloque `location /` de [`deploy/nginx-proxy.conf`](deploy/nginx-proxy.conf) en la configuración del sitio.

### 5. Comprobar

```bash
curl -I https://s.ejemplo.com/healthz
```

Abre `https://s.ejemplo.com`, guarda tu token en **"Acceso y mis links"** y crea el primer link.

## API

Todas las rutas de `/api` aceptan y devuelven JSON. Si `ADMIN_TOKEN` está definido, requieren la cabecera `Authorization: Bearer <token>`.

| Método | Ruta | Descripción |
|---|---|---|
| `POST` | `/api/links` | Crea un link. Cuerpo: `{"url": "...", "alias": "opcional"}` |
| `GET` | `/api/links` | Lista los 200 links más recientes con sus clics |
| `GET` | `/api/links/{código}` | Devuelve un link con sus clics |
| `DELETE` | `/api/links/{código}` | Borra un link |
| `GET` | `/{código}` | Redirige al destino (público) |
| `GET` | `/healthz` | Comprobación de salud (público) |

Crear un link con código aleatorio:

```bash
curl -X POST https://s.ejemplo.com/api/links \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"url": "https://ejemplo.com/una/ruta/muy/larga"}'
```

```json
{
  "code": "aB3xK9p",
  "url": "https://ejemplo.com/una/ruta/muy/larga",
  "created_at": "2026-11-11T05:40:58Z",
  "clicks": 0,
  "short_url": "https://s.ejemplo.com/aB3xK9p"
}
```

Crear un link con alias:

```bash
curl -X POST https://s.ejemplo.com/api/links \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"url": "https://ejemplo.com/ofertas", "alias": "promo"}'
```

Listar, consultar uno y borrar:

```bash
curl https://s.ejemplo.com/api/links -H "Authorization: Bearer $ADMIN_TOKEN"
curl https://s.ejemplo.com/api/links/promo -H "Authorization: Bearer $ADMIN_TOKEN"
curl -X DELETE https://s.ejemplo.com/api/links/promo -H "Authorization: Bearer $ADMIN_TOKEN"
```

Respuestas de error, siempre con la forma `{"error": "mensaje"}`:

| Código | Cuándo |
|---|---|
| `400` | URL inválida, alias con formato incorrecto o alias reservado |
| `401` | Falta el token o no es correcto |
| `404` | El código no existe |
| `409` | El alias ya está en uso |
| `429` | Más de 30 intentos sin token válido por minuto desde la misma IP |

**Reglas de los alias:** de 3 a 32 caracteres, con letras, números, `-` y `_`. Están reservados `api`, `static`, `healthz`, `admin`, `favicon.ico` y `robots.txt`.

## Copias de seguridad

Todos los datos están en un único archivo SQLite. Para copiarlo en caliente sin detener el servicio:

```bash
sqlite3 /opt/acortador/data/acortador.db ".backup '/root/acortador-$(date +%F).db'"
```

## Personalizarlo

| Quieres cambiar | Dónde |
|---|---|
| Aspecto del front | `web/style.css` (colores en las variables de `:root`) |
| Textos de la página | `web/index.html` |
| Longitud de los códigos aleatorios | `codeLength` en `handlers.go` |
| Caracteres de los códigos | `alphabet` en `handlers.go` (sin `l`, `I`, `O`, `0` ni `1` para evitar confusiones) |
| Alias reservados | `reserved` en `handlers.go` |
| Límite de intentos sin token | `newLimiter(30, time.Minute)` en `handlers.go` |

El front se incluye dentro del binario con `go:embed`, así que después de cualquier cambio en `web/` hay que volver a compilar.

## Estructura del proyecto

```text
.
├── main.go            # configuración, arranque y apagado ordenado
├── handlers.go        # rutas HTTP, validación, generación de códigos, autenticación
├── store.go           # acceso a SQLite
├── ratelimit.go       # límite de peticiones por IP
├── main_test.go       # tests
├── web/               # front (HTML, CSS y JS sin frameworks)
└── deploy/
    ├── deploy.sh              # compila y despliega por SSH
    ├── acortador.service      # unidad de systemd
    ├── acortador.env.example  # ejemplo de configuración
    └── nginx-proxy.conf       # bloque de proxy para Nginx/aaPanel
```

## Licencia

[MIT](LICENSE). Puedes usarlo, modificarlo y redistribuirlo libremente, también con fines comerciales, siempre que mantengas el aviso de copyright.
