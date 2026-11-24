package android.util
object Base64 { const val NO_WRAP=0;fun encodeToString(b: ByteArray,flags: Int)=java.util.Base64.getEncoder().encodeToString(b) }
