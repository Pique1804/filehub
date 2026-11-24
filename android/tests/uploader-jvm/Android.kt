package android.content
import java.io.File
class Context {
 companion object { const val MODE_PRIVATE=0 }
 val contentResolver=Resolver()
 private val preferences=Preferences()
 fun getSharedPreferences(name: String, mode: Int)=preferences
}
class Resolver { var opens=0; fun openInputStream(uri: String)=File(uri).inputStream().also { opens++ } }
class Preferences { fun getString(key: String, default: String?)=default;fun edit()=this;fun remove(key: String)=this;fun apply() {} }
