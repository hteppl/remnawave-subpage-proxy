<p align="center">
  <img src="https://raw.githubusercontent.com/hteppl/remnawave-subpage-proxy/master/.github/images/logo.webp" alt="remnawave-subpage-proxy" width="800px">
</p>

## remnawave-subpage-proxy

[![Release](https://img.shields.io/github/v/release/hteppl/remnawave-subpage-proxy?logo=github&logoColor=white&label=release)](https://github.com/hteppl/remnawave-subpage-proxy/releases/latest)
[![Docker Image](https://img.shields.io/docker/v/hteppl/remnawave-subpage-proxy?logo=docker&logoColor=white&label=docker)](https://hub.docker.com/r/hteppl/remnawave-subpage-proxy)
[![Build](https://img.shields.io/github/actions/workflow/status/hteppl/remnawave-subpage-proxy/dockerhub-publish.yaml?logo=githubactions&logoColor=white&label=build)](https://github.com/hteppl/remnawave-subpage-proxy/actions/workflows/dockerhub-publish.yaml)
[![Go](https://img.shields.io/badge/go-1.27-blue.svg?logo=go&logoColor=white)](https://github.com/hteppl/remnawave-subpage-proxy/blob/master/go.mod)
[![License: GPL v3](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](https://github.com/hteppl/remnawave-subpage-proxy/blob/master/LICENSE)

[English](README.md) | Русский

Автоматически подставляет значения переменных в параметрах подписки
Remnawave (https://docs.rw).

Announce задается в панели Remnawave или в конфигурации прокси:

```
Использовано {TRAFFIC_USED} из {TRAFFIC_LIMIT} · осталось {DAYS_LEFT} дн.
```

Клиент получает его с подставленными значениями:

```
Использовано 10.50 GB из 100.00 GB · осталось 12 дн.
```

## Возможности

- **Шаблонные переменные** - Подставляет `{TRAFFIC_USED}`, `{TRAFFIC_AVAILABLE}`, `{DAYS_LEFT}` и другие в параметры
  подписки
- **Бесплатные плейсхолдеры** - Трафик и срок берутся из заголовков ответа, поэтому обычный случай не стоит ни одного
  запроса к API
- **Данные из панели** - Имя, статус и суммарный трафик через Remnawave API, с кэшем и склейкой одинаковых запросов
- **Два источника шаблонов** - Announce можно писать в Custom Response Headers панели или в `config.yaml`
- **Условные правила** - Разный текст для разных типов клиентов, user-agent или статусов
- **Резервный кэш** - Опционально отдает последнюю удачную подписку, пока Remnawave недоступен
- **Принудительный безлимит** - Опционально показывает безлимит независимо от квоты в панели
- **Прозрачное проксирование** - Регистр заголовков, цепочка `X-Forwarded-*` и обрыв соединения при ошибке сохраняются
- **Готов к Docker** - Мультиарх-образ, non-root, только чтение, самопроверка healthcheck

## Требования

Перед началом убедитесь, что у вас есть:

- **Панель Remnawave** с настроенной страницей подписки
- **API-токен Remnawave** - Создается в Remnawave Settings → API Tokens
- **remnawave/subscription-page** - Идет в compose-файле либо уже запущен
- **Docker и Docker Compose**

## Как это работает

Прокси встает перед официальной
[страницей подписки](https://github.com/remnawave/subscription-page) и переписывает
заголовки ответа. Сама страница остается без изменений: веб-интерфейс,
определение браузера, шаблоны под разные клиенты, legacy-ссылки Marzban и
subpage-конфигурации продолжают работать, обновления апстрима остаются
применимыми.

```
клиент → caddy/nginx :443 → subpage-proxy :3020 → subscription-page :3010 → панель
                                  │
                                  └── GET /api/sub/{shortUuid}/info   (только если понадобится)
```

Данные о трафике и сроке действия извлекаются из заголовка
`subscription-userinfo`, сопровождающего каждый ответ подписки, поэтому в обычном
случае **дополнительных запросов к API не требуется**. Обращение к панели
происходит только тогда, когда шаблон использует данные, отсутствующие в
заголовках, — например `{USERNAME}` или `{USER_STATUS}`. Такие запросы
кэшируются, а одновременные одинаковые объединяются в один.

При недоступности панели нераскрытые плейсхолдеры сохраняют исходный текст и не
очищаются, а подписка передается клиенту без изменений.

## Быстрый старт

```bash
git clone https://github.com/hteppl/remnawave-subpage-proxy.git
cd remnawave-subpage-proxy

cp .env.example .env
cp .env.subscription-page.example .env.subscription-page
cp config.example.yaml config.yaml

# Впишите REMNAWAVE_API_TOKEN в оба .env-файла.
# Токен создается в Remnawave Dashboard → Remnawave Settings → API Tokens.

docker compose pull
docker compose up -d
```

`config.yaml` необязателен. Без него прокси заполняет плейсхолдеры, заданные в
Custom Response Headers панели.

Для сборки из исходников вместо готового образа применяется dev-оверрайд:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

Оверрайд собирает тег `:dev` локально, переводит логи в `debug`/`text` и
публикует health-порт на 3021. Эквивалентная сокращенная команда — `make dev`.

Далее обратный прокси перенаправляется на `127.0.0.1:3020` вместо
`127.0.0.1:3010`:

```caddy
sub.example.com {
    reverse_proxy 127.0.0.1:3020
}
```

### Развернутая страница подписки

Используется `docker-compose.proxy-only.yml`; из compose-файла самой страницы
следует убрать проброс `ports:`, чтобы она была доступна только через прокси.

```bash
cp .env.example .env
cp config.example.yaml config.yaml
docker compose -f docker-compose.proxy-only.yml up -d
```

## Заметки для продакшена

Вместо отслеживания `latest` рекомендуется фиксировать конкретную версию в теге
образа в compose-файле. Оба compose-файла запускают контейнер с файловой системой
только для чтения, снятыми capabilities, установленным `no-new-privileges` и
ограничением JSON-лога до 3 × 10 МБ.

`LOG_FORMAT=json` рекомендуется, если логи передаются во внешнюю систему.
`HTTP_SHUTDOWN_TIMEOUT` должен оставаться меньше `stop_grace_period` из compose
(по умолчанию 15s и 20s соответственно), чтобы текущие запросы подписки успевали
завершиться до остановки контейнера.

Панель проверяется однократно при старте. Неудачная проверка фиксируется в логе
на уровне error, но не препятствует работе: подписки отдаются и без панели.

## Настройка announce

Шаблон может задаваться в одном из двух мест; оба источника применяются.

**В панели** — Remnawave Settings → Subscription Settings → Custom
Response Headers:

| Заголовок  | Значение                                         |
|------------|--------------------------------------------------|
| `announce` | `Использовано {TRAFFIC_USED} из {TRAFFIC_LIMIT}` |

Прокси подставляет значения в исходящем ответе. Дополнительной настройки не
требуется: `scan_all_headers` включен по умолчанию, поэтому механизм действует
для любого заголовка, устанавливаемого панелью, а не только для `announce`.
Значения, закодированные панелью в base64, декодируются, заполняются и
кодируются обратно в том же виде.

**В `config.yaml`** — для шаблонов, хранимых в системе контроля версий, либо
требующих условий:

```yaml
vars:
  BRAND: "MyProject"
  SUPPORT: "@my_support_bot"

headers:
  - name: announce
    template: "{BRAND} · израсходовано {TRAFFIC_USED} из {TRAFFIC_LIMIT} · осталось {DAYS_LEFT} дн. · {SUPPORT}"
    encode: base64-prefixed   # требуется для Happ
    max_length: 200           # Happ показывает максимум 200 символов

  # Альтернативный текст при исчерпании лимита.
  - name: profile-title
    template: "{BRAND} — {TRAFFIC_AVAILABLE}"
    encode: base64
    max_length: 25
```

Все доступные параметры описаны в [`config.example.yaml`](config.example.yaml).

## Плейсхолдеры

Синтаксис — `{NAME}`. Модификаторы необязательны и объединяются в цепочку:
`{NAME|upper}`, `{NAME|lower}`, `{NAME|trim}`, `{NAME|truncate:40}`,
`{NAME|default:n/a}`.

Имена записываются в `UPPER_SNAKE_CASE`. Благодаря этому JSON- и Clash-конфигурации,
проходящие через прокси, не интерпретируются как шаблоны.

### Разрешаются без запроса к панели

| Плейсхолдер                 | Пример                                   |
|-----------------------------|------------------------------------------|
| `{TRAFFIC_USED}`            | `10.50 GB`                               |
| `{TRAFFIC_LIMIT}`           | `100.00 GB`                              |
| `{TRAFFIC_AVAILABLE}`       | `89.50 GB` (лимит минус израсходованное) |
| `{TRAFFIC_USED_BYTES}`      | `10500000000`                            |
| `{TRAFFIC_LIMIT_BYTES}`     | `100000000000`                           |
| `{TRAFFIC_AVAILABLE_BYTES}` | `89500000000`                            |
| `{TRAFFIC_UPLOAD}`          | `0.50 GB`                                |
| `{TRAFFIC_DOWNLOAD}`        | `10.00 GB`                               |
| `{TRAFFIC_USED_PERCENT}`    | `10`                                     |
| `{TRAFFIC_LEFT_PERCENT}`    | `90`                                     |
| `{PROGRESS_BAR}`            | `▰▱▱▱▱▱▱▱▱▱`                             |
| `{DAYS_LEFT}`               | `12`                                     |
| `{EXPIRES_AT}`              | `31.12.2026 23:59`                       |
| `{EXPIRES_AT_DATE}`         | `31.12.2026`                             |
| `{EXPIRES_AT_TIME}`         | `23:59`                                  |
| `{EXPIRES_AT_UNIX}`         | `1798761599`                             |
| `{SHORT_UUID}`              | `aBcDeF123`                              |
| `{CLIENT_TYPE}`             | `clash`                                  |
| `{USER_AGENT}`              | `Happ/1.0`                               |
| `{CLIENT_IP}`               | `203.0.113.9`                            |
| `{NOW}` `{DATE}` `{TIME}`   | `01.09.2026 14:30`                       |
| `{SUBSCRIPTION_URL}`        | `https://example.com/sub/aBcDeF123`      |

`{SUBSCRIPTION_URL}` формируется из самого запроса — схемы и хоста из
`X-Forwarded-*` плюс короткого UUID, — поэтому содержит адрес, по которому
обратился клиент, и не требует запроса к панели. Сегмент с типом клиента
отбрасывается: из `/{shortUuid}/clash` формируется ссылка `/{shortUuid}`.

На безлимитном тарифе `{TRAFFIC_LIMIT}` и `{TRAFFIC_AVAILABLE}` отображаются как
`∞` (текст настраивается); у бессрочной подписки аналогично отображается
`{EXPIRES_AT}`.

### Требуют запроса к панели (результат кэшируется)

| Плейсхолдер                | Пример                                          |
|----------------------------|-------------------------------------------------|
| `{USERNAME}`               | `alice`                                         |
| `{USER_STATUS}`            | `ACTIVE` `DISABLED` `LIMITED` `EXPIRED`         |
| `{IS_ACTIVE}`              | `true`                                          |
| `{TRAFFIC_LIMIT_STRATEGY}` | `NO_RESET` `DAY` `WEEK` `MONTH` `MONTH_ROLLING` |
| `{LIFETIME_TRAFFIC_USED}`  | `1.20 TB`                                       |

Дополнительно — любые переменные, заданные в разделе `vars:` файла `config.yaml`.

## Условия

Правила могут ограничиваться так, чтобы разные клиенты или пользователи получали
разный текст:

```yaml
headers:
  # Только Happ.
  - name: announce
    template: "{PROGRESS_BAR} израсходовано {TRAFFIC_USED_PERCENT}%"
    encode: base64-prefixed
    when:
      user_agent: "(?i)happ"

  # Только пути с типом клиента /json и /clash.
  - name: X-Plan-Summary
    template: "{TRAFFIC_USED} / {TRAFFIC_LIMIT}"
    when:
      client_types: [ json, clash ]

  # Пользователи, исчерпавшие лимит трафика.
  - name: announce
    template: "Лимит трафика исчерпан — продлите на {SUPPORT}"
    encode: base64-prefixed
    when:
      user_statuses: [ LIMITED, EXPIRED ]

  # Тарифы с ограниченной квотой.
  - name: announce
    template: "израсходовано {TRAFFIC_USED} из {TRAFFIC_LIMIT}"
    encode: base64-prefixed
    when:
      has_traffic_limit: true

  # Безлимитные тарифы.
  - name: x-plan
    template: "Безлимитный трафик"
    when:
      has_traffic_limit: false

  # Значение по умолчанию, применяется только если панель не прислала заголовок.
  - name: support-url
    template: "https://t.me/my_support_bot"
    when:
      exists: false
```

`has_traffic_limit` отличает ограниченную квоту от безлимитного тарифа, который
Remnawave кодирует нулевым значением total. Значение читается из заголовка
`subscription-userinfo`, поэтому обычно не требует запроса к панели; панель
опрашивается, только если в заголовке нет поля total. Правило пропускается, если
квоту определить не удалось.

`traffic.force_unlimited` на это условие не влияет. Он меняет то, что видит
клиент, тогда как `has_traffic_limit` проверяет квоту, реально заданную в панели,
поэтому их можно сочетать: тариф с лимитом можно показывать как безлимитный и
при этом ловить его условием `has_traffic_limit: true`.

`user_statuses` всегда вызывает запрос к панели, поскольку статус отсутствует в
заголовках ответа.

## Справочник конфигурации

Инфраструктура настраивается переменными окружения, шаблонизация — через
`config.yaml`. В таблице перечислены все переменные со значениями, действующими
при отсутствии явной настройки; в поставляемом [`.env.example`](.env.example)
часть из них переопределена.

| Переменная                            | По умолчанию  | Назначение                                                           |
|---------------------------------------|---------------|----------------------------------------------------------------------|
| `UPSTREAM_URL`                        | —             | Страница подписки, которую проксируем. **Обязательная.**             |
| `REMNAWAVE_PANEL_URL`                 | —             | Адрес панели. Обязательная, если `PANEL_ENABLED` не выключен.        |
| `REMNAWAVE_API_TOKEN`                 | —             | API-токен панели. Обязательная, если `PANEL_ENABLED` не выключен.    |
| `APP_HOST`                            | `0.0.0.0`     | Адрес, на котором слушает прокси.                                    |
| `APP_PORT`                            | `3020`        | Публичный порт.                                                      |
| `HEALTH_HOST`                         | `0.0.0.0`     | Адрес для health-эндпоинтов.                                         |
| `HEALTH_PORT`                         | `3021`        | Порт health-эндпоинтов. `0` — выключить оба.                         |
| `CONFIG_PATH`                         | `config.yaml` | Файл с правилами. Отсутствие файла — ошибка, только если задан явно. |
| `CUSTOM_SUB_PREFIX`                   | —             | Префикс пути. Должен совпадать с настройкой страницы подписки.       |
| `TRUST_PROXY`                         | `1`           | `true`/`false`, количество хопов либо список пресетов, IP и CIDR.    |
| `UPSTREAM_FORCE_HTTPS`                | `false`       | Всегда слать апстриму `X-Forwarded-Proto: https`.                    |
| `PANEL_ENABLED`                       | `true`        | `false` — работать без доступов к панели.                            |
| `PANEL_ALWAYS_FETCH`                  | `false`       | Ходить в панель на каждую подписку, даже если это никому не нужно.   |
| `PANEL_FORWARD_REAL_IP`               | `false`       | Передавать IP пользователя при запросе info.                         |
| `PANEL_TIMEOUT`                       | `10s`         | Таймаут одного запроса к API панели.                                 |
| `CACHE_TTL`                           | `30s`         | Сколько переиспользуется успешный ответ панели.                      |
| `CACHE_NEGATIVE_TTL`                  | `10s`         | Сколько помнится ответ «не найдено».                                 |
| `CACHE_MAX_ENTRIES`                   | `10000`       | Максимум записей в кэше запросов.                                    |
| `CADDY_AUTH_API_TOKEN`                | —             | Уходит как `X-Api-Key`, если панель за Caddy security.               |
| `CLOUDFLARE_ZERO_TRUST_CLIENT_ID`     | —             | `CF-Access-Client-Id`, если панель за Cloudflare Zero Trust.         |
| `CLOUDFLARE_ZERO_TRUST_CLIENT_SECRET` | —             | `CF-Access-Client-Secret` для того же случая.                        |
| `SUBSCRIPTION_CACHE_ENABLED`          | `false`       | Отдавать последний удачный ответ, пока Remnawave недоступен.         |
| `SUBSCRIPTION_CACHE_TTL`              | `1h`          | Сколько сохраненный ответ остается пригодным.                        |
| `SUBSCRIPTION_CACHE_MAX_BYTES`        | `64MiB`       | Общий бюджет памяти под резервный кэш.                               |
| `SUBSCRIPTION_CACHE_MAX_BODY`         | `1MiB`        | Максимальный размер одного сохраняемого ответа.                      |
| `UPSTREAM_TIMEOUT`                    | `60s`         | Ожидание заголовков ответа от апстрима.                              |
| `HTTP_READ_TIMEOUT`                   | `30s`         | Чтение запроса клиента.                                              |
| `HTTP_WRITE_TIMEOUT`                  | `90s`         | Запись ответа.                                                       |
| `HTTP_IDLE_TIMEOUT`                   | `120s`        | Простой keep-alive соединения.                                       |
| `HTTP_SHUTDOWN_TIMEOUT`               | `15s`         | Завершение по SIGTERM. Держите меньше `stop_grace_period`.           |
| `LOG_LEVEL`                           | `info`        | На `debug` пишет каждую перезапись заголовка.                        |
| `LOG_FORMAT`                          | `text`        | `text` или `json`.                                                   |

Длительность без единицы измерения интерпретируется как секунды, поэтому
`CACHE_TTL=45` и `CACHE_TTL=45s` эквивалентны. Размер принимает `1MiB`, `512KB`
или число без суффикса.

### Принудительный безлимит

`traffic.force_unlimited` сообщает клиенту о безлимитном тарифе независимо от
квоты, заданной в панели:

```yaml
traffic:
  force_unlimited: true
```

Заголовок `subscription-userinfo` передается со значением `total=0` — стандартной
кодировкой безлимитного тарифа, определяющей отображение остатка трафика в
клиентских приложениях. `{TRAFFIC_LIMIT}` и `{TRAFFIC_AVAILABLE}` при этом
отображаются как `∞`, `{TRAFFIC_USED_PERCENT}` — как `0`, а `{PROGRESS_BAR}`
остается пустым.

Израсходованный трафик и срок действия не изменяются: `{TRAFFIC_USED}` продолжает
отражать фактическое потребление, подписка завершается в назначенный срок.
Скрывается только лимит.

### Резервный кэш подписок

По умолчанию отключен, включается параметром `SUBSCRIPTION_CACHE_ENABLED=true`.
Прокси сохраняет последний успешный ответ подписки для каждого клиента и отдает
его, пока Remnawave недоступен, благодаря чему у существующих пользователей
сохраняется рабочая конфигурация на время недоступности панели или перезапуска
страницы.

Это резервный, а не сквозной кэш: каждый запрос сначала направляется в апстрим,
и сохраненная копия используется только при его неудаче — обрыве соединения,
таймауте или ответе 5xx. Пока Remnawave функционирует штатно, устаревшие данные
клиенту не передаются.

Записи различаются по короткому UUID, типу клиента, `User-Agent` и
`Accept-Encoding`, поскольку Remnawave формирует разную нагрузку для разных
клиентов. Сохраняется завершенный ответ, поэтому в отданном из кэша announce
содержатся значения, актуальные на момент сохранения. Веб-страница не
кэшируется, только сами подписки.

Следует учитывать два следствия: счетчики трафика в таком ответе соответствуют
возрасту записи, а пользователь, отключенный во время аварии, сохраняет доступ до
истечения TTL. Значение `SUBSCRIPTION_CACHE_TTL` подбирается с учетом этого.

### Проверки состояния

`/healthz` на `HEALTH_PORT` отвечает за liveness, `/readyz` дополнительно
проверяет доступность апстрима. Оба вынесены на отдельный порт, чтобы ни один
путь запроса не мог перекрыть короткий UUID подписки. `HEALTHCHECK` контейнера
обращается к этому эндпоинту тем же бинарником, поэтому образу не требуются ни
шелл, ни `curl`.

## Особенности поведения

- **Регистр заголовков сохраняется.** Разбирая ответ, Go приводит имена
  заголовков к каноническому виду (`announce` → `Announce`). Прокси возвращает
  нижний регистр, в котором их отдает страница подписки, поэтому клиенты получают
  то же написание заголовков, что и до внедрения прокси.
- **Обработка base64 безопасна.** Значение декодируется и кодируется обратно
  только в том случае, если внутри обнаружен плейсхолдер. Непрозрачные
  base64-данные, как и обычный текст, случайно оказавшийся валидным base64,
  передаются байт в байт.
- **Запросы, которые апстрим не принимает, обрываются, а не обслуживаются.**
  Страница подписки разрывает сокет на некорректных запросах, не предоставляя
  сканерам никакой информации; прокси повторяет это поведение и фиксирует причину
  в логе на уровне `warn`.
- **`X-Forwarded-*` дополняется, а не перезатирается.** Входящая цепочка
  сохраняется, текущий хоп дописывается в конец — поэтому страница подписки
  определяет настоящего клиента своим же `TRUST_PROXY=1`.
- **Панель не находится на критическом пути.** При ее недоступности подписки
  продолжают отдаваться, а зависимые от нее плейсхолдеры сохраняют исходный текст.
- **Legacy-ссылки Marzban работают частично.** В сегменте пути у них непрозрачный
  токен, а не короткий UUID, так что найти пользователя в панели прокси не может.
  Трафик и срок действия подставляются, поскольку приходят в заголовке ответа;
  `{USERNAME}` и прочие данные из панели — нет.

## Лицензия

Проект распространяется под [GNU General Public License v3.0](LICENSE).
