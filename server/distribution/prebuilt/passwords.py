"""Small libxcrypt bcrypt binding, including Python 3.13+ without crypt module."""
import ctypes
import ctypes.util
import secrets
import threading


class LibCrypt:
    METHOD_BLOWFISH = 'bcrypt'

    def __init__(self):
        library = ctypes.util.find_library('crypt')
        if not library:
            raise RuntimeError('Install libcrypt1 (libxcrypt) before starting Filehub')
        self.library = ctypes.CDLL(library)
        self.library.crypt.argtypes = [ctypes.c_char_p, ctypes.c_char_p]
        self.library.crypt.restype = ctypes.c_char_p
        self.library.crypt_gensalt_rn.argtypes = [ctypes.c_char_p, ctypes.c_ulong, ctypes.c_char_p,
                                               ctypes.c_int, ctypes.c_char_p, ctypes.c_int]
        self.library.crypt_gensalt_rn.restype = ctypes.c_char_p
        # crypt() returns a shared buffer. Copy it before releasing this lock.
        self.lock = threading.Lock()

    def mksalt(self, method=METHOD_BLOWFISH, rounds=1024):
        if method != self.METHOD_BLOWFISH or rounds < 16 or rounds & (rounds - 1):
            raise ValueError('Invalid bcrypt cost')
        output = ctypes.create_string_buffer(128)
        entropy = secrets.token_bytes(16)
        result = self.library.crypt_gensalt_rn(b'$2b$', rounds.bit_length()-1, entropy, len(entropy), output, len(output))
        if not result:
            raise RuntimeError('libxcrypt does not support bcrypt')
        return output.value.decode('ascii')

    def crypt(self, password, salt):
        if '\x00' in password or '\x00' in salt:
            raise ValueError('Password contains a null byte')
        with self.lock:
            value = self.library.crypt(password.encode('utf-8'), salt.encode('ascii'))
            return value.decode('ascii') if value and not value.startswith(b'*') else None


crypt = LibCrypt()
