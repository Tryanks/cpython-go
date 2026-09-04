"""Stand-in for CPython's macOS _scproxy extension (needs the
SystemConfiguration framework, which the pure Go build cannot link).

urllib.request imports it on darwin to read the system proxy settings; this
version reports "no system proxies", so only the *_proxy environment
variables apply.
"""


def _get_proxy_settings():
    return {"exclude_simple": False}


def _get_proxies():
    return {}
