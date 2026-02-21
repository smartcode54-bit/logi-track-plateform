import 'package:flutter/material.dart';
import 'package:easy_localization/easy_localization.dart';

class DeliveryPhasePage extends StatefulWidget {
  const DeliveryPhasePage({super.key});

  @override
  State<DeliveryPhasePage> createState() => _DeliveryPhasePageState();
}

class _DeliveryPhasePageState extends State<DeliveryPhasePage> {
  // Mock states for the 4 required photos
  bool _hasBeforeOpenPhoto = false;
  bool _hasDuringOpenPhoto = false;
  bool _hasEmptyContainerPhoto = false;
  bool _hasReceivedScreenshot = false;

  bool get _canSubmit =>
      _hasBeforeOpenPhoto &&
      _hasDuringOpenPhoto &&
      _hasEmptyContainerPhoto &&
      _hasReceivedScreenshot;

  @override
  Widget build(BuildContext context) {
    final isDarkMode = Theme.of(context).brightness == Brightness.dark;
    const darkNavy = Color(0xFF0F172A);

    return Scaffold(
      backgroundColor: isDarkMode
          ? Theme.of(context).scaffoldBackgroundColor
          : Colors.grey[50],
      appBar: AppBar(
        title: Text(
          'nav_delivery'.tr(),
          style: const TextStyle(color: Colors.white),
        ),
        backgroundColor: darkNavy,
        iconTheme: const IconThemeData(color: Colors.white),
        actions: [
          TextButton.icon(
            onPressed: () {
              // TODO: Handle incident reporting logic
            },
            icon: const Icon(Icons.warning_amber_rounded, color: Colors.orange),
            label: Text(
              'report_incident'.tr(),
              style: const TextStyle(
                color: Colors.orange,
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
        ],
      ),
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            // Header Summary Card
            Card(
              elevation: 4,
              color: darkNavy,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(16),
              ),
              child: Padding(
                padding: const EdgeInsets.all(20.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        Text(
                          'active_delivery'.tr(),
                          style: const TextStyle(
                            color: Colors.white70,
                            fontSize: 16,
                            fontWeight: FontWeight.w500,
                          ),
                        ),
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 12,
                            vertical: 4,
                          ),
                          decoration: BoxDecoration(
                            color: Colors.green.withOpacity(0.2),
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: Colors.green.shade300),
                          ),
                          child: Text(
                            'status_in_transit'.tr(),
                            style: const TextStyle(
                              color: Colors.greenAccent,
                              fontWeight: FontWeight.bold,
                              fontSize: 12,
                            ),
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 16),
                    const Text(
                      'Shopee Express - Chiang Mai DC',
                      style: TextStyle(
                        color: Colors.white,
                        fontSize: 20,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                    const SizedBox(height: 8),
                    const Text(
                      'Trip ID: #TRP-9042',
                      style: TextStyle(color: Colors.white54, fontSize: 14),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 24),

            // Checklist Header
            Text(
              'mandatory_evidence'.tr(),
              style: Theme.of(context).textTheme.titleLarge?.copyWith(
                fontWeight: FontWeight.bold,
                color: isDarkMode ? Colors.white : darkNavy,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              'mandatory_evidence_desc'.tr(),
              style: TextStyle(
                color: isDarkMode ? Colors.grey[400] : Colors.grey[600],
                fontSize: 14,
              ),
            ),
            const SizedBox(height: 16),

            // Photo Checklist Cards
            _buildPhotoUploadTile(
              title: 'delivery_photo_pre_open'.tr(),
              subtitle: 'delivery_photo_pre_open_desc'.tr(),
              isCaptured: _hasBeforeOpenPhoto,
              onTap: () {
                setState(() => _hasBeforeOpenPhoto = !_hasBeforeOpenPhoto);
              },
            ),
            const SizedBox(height: 12),
            _buildPhotoUploadTile(
              title: 'delivery_photo_opening'.tr(),
              subtitle: 'delivery_photo_opening_desc'.tr(),
              isCaptured: _hasDuringOpenPhoto,
              onTap: () {
                setState(() => _hasDuringOpenPhoto = !_hasDuringOpenPhoto);
              },
            ),
            const SizedBox(height: 12),
            _buildPhotoUploadTile(
              title: 'delivery_photo_empty'.tr(),
              subtitle: 'delivery_photo_empty_desc'.tr(),
              isCaptured: _hasEmptyContainerPhoto,
              onTap: () {
                setState(
                  () => _hasEmptyContainerPhoto = !_hasEmptyContainerPhoto,
                );
              },
            ),
            const SizedBox(height: 12),
            _buildPhotoUploadTile(
              title: 'delivery_photo_received'.tr(),
              subtitle: 'delivery_photo_received_desc'.tr(),
              isCaptured: _hasReceivedScreenshot,
              onTap: () {
                setState(
                  () => _hasReceivedScreenshot = !_hasReceivedScreenshot,
                );
              },
              icon: Icons.screenshot_monitor,
            ),

            const SizedBox(height: 32),

            // Submit Button
            ElevatedButton(
              onPressed: _canSubmit
                  ? () {
                      // TODO: Handle submit delivery transition to DELIVERED
                    }
                  : null,
              style: ElevatedButton.styleFrom(
                backgroundColor: Colors.blueAccent,
                padding: const EdgeInsets.symmetric(vertical: 16),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
                disabledBackgroundColor: Colors.grey.shade400,
              ),
              child: Text(
                'submit_delivery'.tr(),
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.bold,
                  color: Colors.white,
                ),
              ),
            ),
            const SizedBox(height: 24),
          ],
        ),
      ),
    );
  }

  Widget _buildPhotoUploadTile({
    required String title,
    required String subtitle,
    required bool isCaptured,
    required VoidCallback onTap,
    IconData icon = Icons.camera_alt,
  }) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(12),
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: isCaptured
              ? Colors.green.withOpacity(0.05)
              : Theme.of(context).cardColor,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(
            color: isCaptured ? Colors.green : Colors.grey.shade300,
            width: 1.5,
          ),
        ),
        child: Row(
          children: [
            Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: isCaptured
                    ? Colors.green
                    : Colors.blueAccent.withOpacity(0.1),
                shape: BoxShape.circle,
              ),
              child: Icon(
                isCaptured ? Icons.check : icon,
                color: isCaptured ? Colors.white : Colors.blueAccent,
              ),
            ),
            const SizedBox(width: 16),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: const TextStyle(
                      fontWeight: FontWeight.bold,
                      fontSize: 16,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    subtitle,
                    style: TextStyle(color: Colors.grey.shade600, fontSize: 12),
                  ),
                ],
              ),
            ),
            if (isCaptured)
              const Icon(Icons.arrow_forward_ios, color: Colors.green, size: 16)
            else
              Icon(
                Icons.arrow_forward_ios,
                color: Colors.grey.shade400,
                size: 16,
              ),
          ],
        ),
      ),
    );
  }
}
