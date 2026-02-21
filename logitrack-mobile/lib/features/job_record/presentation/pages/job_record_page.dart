import 'package:flutter/material.dart';
import 'package:easy_localization/easy_localization.dart';
import '../../../../components/quick_action_card.dart';

class JobRecordPage extends StatelessWidget {
  const JobRecordPage({super.key});

  @override
  Widget build(BuildContext context) {
    // Determine if dark mode so we don't force dark navy on light themes
    // unless explicitly requested by design. The prompt mentioned "dark navy color palette".
    // We will use a dedicated dark navy theme for the primary elements here.
    final isDarkMode = Theme.of(context).brightness == Brightness.dark;

    // Dark Navy core colors
    const darkNavy = Color(0xFF0F172A); // Slate 900

    return Scaffold(
      backgroundColor: isDarkMode
          ? Theme.of(context).scaffoldBackgroundColor
          : Colors.grey[50],
      appBar: AppBar(
        title: Text(
          'Job Record'.tr(),
          style: const TextStyle(color: Colors.white),
        ),
        backgroundColor: darkNavy,
        iconTheme: const IconThemeData(color: Colors.white),
      ),
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            // Header Summary Card matching home screen styling but with Navy palette
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
                          'Current Job'.tr(),
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
                            color: Colors.blue.withOpacity(0.2),
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: Colors.blue.shade300),
                          ),
                          child: const Text(
                            'In Progress',
                            style: TextStyle(
                              color: Colors.blue,
                              fontWeight: FontWeight.bold,
                              fontSize: 12,
                            ),
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 16),
                    const Text(
                      'BKK Hub → Chiang Mai',
                      style: TextStyle(
                        color: Colors.white,
                        fontSize: 24,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                    const SizedBox(height: 8),
                    const Text(
                      'Job ID: #TRK-8821',
                      style: TextStyle(color: Colors.white54, fontSize: 14),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 24),

            Text(
              'Job Actions'.tr(),
              style: Theme.of(context).textTheme.titleLarge?.copyWith(
                fontWeight: FontWeight.bold,
                color: isDarkMode ? Colors.white : darkNavy,
              ),
            ),
            const SizedBox(height: 16),

            // Reusing QuickActionCard with custom colors
            GridView.count(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              crossAxisCount: 2,
              mainAxisSpacing: 16.0,
              crossAxisSpacing: 16.0,
              childAspectRatio: 1.2,
              children: [
                QuickActionCard(
                  icon: Icons.camera_alt,
                  label: 'Update Status'.tr(),
                  iconColor: Colors.blueAccent,
                  onTap: () {},
                ),
                QuickActionCard(
                  icon: Icons.receipt_long,
                  label: 'Upload POD'.tr(),
                  iconColor: Colors.green,
                  onTap: () {},
                ),
                QuickActionCard(
                  icon: Icons.map,
                  label: 'Route Map'.tr(),
                  iconColor: Colors.purple,
                  onTap: () {},
                ),
                QuickActionCard(
                  icon: Icons.chat_bubble_outline,
                  label: 'Contact Hub'.tr(),
                  iconColor: Colors.orange,
                  onTap: () {},
                ),
              ],
            ),
            const SizedBox(height: 32),

            // Secondary Card
            Card(
              elevation: 2,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
              child: Padding(
                padding: const EdgeInsets.all(16.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Job Details'.tr(),
                      style: const TextStyle(
                        fontSize: 18,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                    const Divider(height: 24),
                    _buildDetailRow('Client', 'Acme Logistics'),
                    const SizedBox(height: 12),
                    _buildDetailRow('Cargo Type', 'General Freight'),
                    const SizedBox(height: 12),
                    _buildDetailRow('Est. Arrival', 'Today, 18:00'),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildDetailRow(String label, String value) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Text(label, style: const TextStyle(color: Colors.grey)),
        Text(value, style: const TextStyle(fontWeight: FontWeight.w500)),
      ],
    );
  }
}
