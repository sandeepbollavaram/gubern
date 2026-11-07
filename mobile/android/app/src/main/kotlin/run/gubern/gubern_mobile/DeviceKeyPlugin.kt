package run.gubern.gubern_mobile

import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import android.util.Base64
import androidx.biometric.BiometricManager.Authenticators
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.spec.ECGenParameterSpec

// The phone's Gubern device key (milestone H): an EC P-256 key generated in
// the Android Keystore (StrongBox when the phone has it). The private key
// never leaves the secure hardware, and every signature needs the person's
// biometrics or device credential. The public key is sent at pairing as a
// base64 SubjectPublicKeyInfo; signatures are base64 ASN.1 ECDSA over
// SHA-256, which the server verifies (identity.VerifyDeviceSignature).
class DeviceKeyPlugin(private val activity: FragmentActivity) {
    private val alias = "run.gubern.devicekey.v1"

    fun register(messenger: BinaryMessenger) {
        MethodChannel(messenger, "run.gubern/devicekey").setMethodCallHandler { call, result ->
            try {
                when (call.method) {
                    "publicKey" -> result.success(publicKey())
                    "sign" -> sign(call.argument<String>("message")!!, call.argument<String>("reason") ?: "Approve", result)
                    "delete" -> {
                        keyStore().deleteEntry(alias)
                        result.success(null)
                    }
                    else -> result.notImplemented()
                }
            } catch (e: Exception) {
                result.error("devicekey", e.message, null)
            }
        }
    }

    private fun keyStore(): KeyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

    private fun ensureKey() {
        if (keyStore().containsAlias(alias)) return
        fun spec(strongBox: Boolean) = KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setDigests(KeyProperties.DIGEST_SHA256)
            .setUserAuthenticationRequired(true)
            .setInvalidatedByBiometricEnrollment(true)
            .apply {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    // Every use needs a fresh authentication (timeout 0).
                    setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG or KeyProperties.AUTH_DEVICE_CREDENTIAL)
                }
                if (strongBox) setIsStrongBoxBacked(true)
            }
            .build()
        val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
        try {
            gen.initialize(spec(true))
            gen.generateKeyPair()
        } catch (e: StrongBoxUnavailableException) {
            gen.initialize(spec(false))
            gen.generateKeyPair()
        }
    }

    private fun publicKey(): String {
        ensureKey()
        val cert = keyStore().getCertificate(alias)
        return Base64.encodeToString(cert.publicKey.encoded, Base64.NO_WRAP)
    }

    private fun sign(message: String, reason: String, result: MethodChannel.Result) {
        ensureKey()
        val key = keyStore().getKey(alias, null) as PrivateKey
        val sig = Signature.getInstance("SHA256withECDSA").apply { initSign(key) }
        val prompt = BiometricPrompt(
            activity, ContextCompat.getMainExecutor(activity),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(r: BiometricPrompt.AuthenticationResult) {
                    try {
                        val s = r.cryptoObject!!.signature!!
                        s.update(message.toByteArray(Charsets.UTF_8))
                        result.success(Base64.encodeToString(s.sign(), Base64.NO_WRAP))
                    } catch (e: Exception) {
                        result.error("devicekey", e.message, null)
                    }
                }

                override fun onAuthenticationError(code: Int, msg: CharSequence) {
                    result.error("cancelled", msg.toString(), null)
                }
            },
        )
        val info = BiometricPrompt.PromptInfo.Builder()
            .setTitle("Gubern")
            .setSubtitle(reason)
            .setAllowedAuthenticators(Authenticators.BIOMETRIC_STRONG or Authenticators.DEVICE_CREDENTIAL)
            .build()
        prompt.authenticate(info, BiometricPrompt.CryptoObject(sig))
    }
}
