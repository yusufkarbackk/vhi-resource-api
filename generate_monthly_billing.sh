#!/bin/bash

# ==============================================================================
# VHI Billing Automation Script
# ==============================================================================
# Script untuk mengotomasi penagihan bulanan ke customer
# 
# Usage:
#   ./generate_monthly_billing.sh [YYYY-MM]
#   
# Example:
#   ./generate_monthly_billing.sh 2026-01
#
# ==============================================================================

set -e

# Configuration
API_BASE_URL="${API_BASE_URL:-http://localhost:8080/api/v1}"
OUTPUT_DIR="${OUTPUT_DIR:-./billing_reports}"
CPU_PRICE="${CPU_PRICE:-0.08}"
MEMORY_PRICE="${MEMORY_PRICE:-0.015}"

# Parse arguments
if [ -z "$1" ]; then
    # Default to last month
    YEAR_MONTH=$(date -d "last month" +%Y-%m)
else
    YEAR_MONTH=$1
fi

# Calculate date range
START_DATE="${YEAR_MONTH}-01T00:00:00"
# Get last day of month
LAST_DAY=$(date -d "${YEAR_MONTH}-01 +1 month -1 day" +%d)
END_DATE="${YEAR_MONTH}-${LAST_DAY}T23:59:59"

echo "========================================="
echo "VHI Monthly Billing Report Generator"
echo "========================================="
echo "Period: ${YEAR_MONTH}"
echo "Start:  ${START_DATE}"
echo "End:    ${END_DATE}"
echo "========================================="

# Create output directory
mkdir -p "${OUTPUT_DIR}/${YEAR_MONTH}"

# List of instance IDs to bill (should come from database or config)
# Example format: "INSTANCE_ID:CUSTOMER_NAME"
INSTANCES=(
    "c921ed74-48e5-4fa6-b093-22d08bdda660:Customer_ABC"
    "a1b2c3d4-e5f6-7890-abcd-ef1234567890:Customer_XYZ"
    # Add more instances here
)

# Generate reports
TOTAL_REVENUE=0
REPORT_COUNT=0

for ENTRY in "${INSTANCES[@]}"; do
    IFS=':' read -r INSTANCE_ID CUSTOMER_NAME <<< "$ENTRY"
    
    echo ""
    echo "Processing: ${CUSTOMER_NAME} (${INSTANCE_ID})"
    
    # Get billing report from API
    REPORT=$(curl -s "${API_BASE_URL}/billing/report/${INSTANCE_ID}?\
start_date=${START_DATE}&\
end_date=${END_DATE}&\
cpu_price_per_hour=${CPU_PRICE}&\
memory_price_per_gb=${MEMORY_PRICE}")
    
    if [ $? -ne 0 ]; then
        echo "  ❌ Failed to get report for ${CUSTOMER_NAME}"
        continue
    fi
    
    # Extract data using jq
    INSTANCE_NAME=$(echo "$REPORT" | jq -r '.instance_name')
    FLAVOR=$(echo "$REPORT" | jq -r '.flavor_name')
    VCPUS=$(echo "$REPORT" | jq -r '.vcpus')
    CPU_COST=$(echo "$REPORT" | jq -r '.cpu_cost')
    MEMORY_COST=$(echo "$REPORT" | jq -r '.memory_cost')
    TOTAL_COST=$(echo "$REPORT" | jq -r '.total_cost')
    AVG_CPU=$(echo "$REPORT" | jq -r '.cpu_usage.average_percent')
    AVG_MEM_GB=$(echo "$REPORT" | jq -r '.memory_usage.average_used_gb')
    
    # Save full JSON report
    REPORT_FILE="${OUTPUT_DIR}/${YEAR_MONTH}/${CUSTOMER_NAME}_${INSTANCE_ID}_report.json"
    echo "$REPORT" | jq '.' > "$REPORT_FILE"
    
    # Generate human-readable invoice
    INVOICE_FILE="${OUTPUT_DIR}/${YEAR_MONTH}/${CUSTOMER_NAME}_${INSTANCE_ID}_invoice.txt"
    cat > "$INVOICE_FILE" <<EOF
================================================================================
                            INVOICE
================================================================================

Customer:           ${CUSTOMER_NAME}
Invoice Date:       $(date +%Y-%m-%d)
Billing Period:     ${YEAR_MONTH}

--------------------------------------------------------------------------------
SERVICE DETAILS
--------------------------------------------------------------------------------

Instance Name:      ${INSTANCE_NAME}
Instance ID:        ${INSTANCE_ID}
Flavor:             ${FLAVOR}
vCPUs:              ${VCPUS}

--------------------------------------------------------------------------------
RESOURCE USAGE
--------------------------------------------------------------------------------

Average CPU Usage:  ${AVG_CPU}%
Average Memory:     ${AVG_MEM_GB} GB

--------------------------------------------------------------------------------
CHARGES
--------------------------------------------------------------------------------

CPU Cost:           \$${CPU_COST}
Memory Cost:        \$${MEMORY_COST}
                    --------
TOTAL:              \$${TOTAL_COST}

--------------------------------------------------------------------------------

Payment Terms: Net 30
Payment Method: Bank Transfer / Credit Card

Thank you for your business!

================================================================================
EOF
    
    # Generate CSV entry for accounting
    CSV_FILE="${OUTPUT_DIR}/${YEAR_MONTH}/billing_summary.csv"
    if [ ! -f "$CSV_FILE" ]; then
        echo "Customer,Instance_ID,Instance_Name,Flavor,vCPUs,Avg_CPU_%,Avg_Memory_GB,CPU_Cost,Memory_Cost,Total_Cost,Period" > "$CSV_FILE"
    fi
    echo "${CUSTOMER_NAME},${INSTANCE_ID},${INSTANCE_NAME},${FLAVOR},${VCPUS},${AVG_CPU},${AVG_MEM_GB},${CPU_COST},${MEMORY_COST},${TOTAL_COST},${YEAR_MONTH}" >> "$CSV_FILE"
    
    # Update totals
    TOTAL_REVENUE=$(echo "$TOTAL_REVENUE + $TOTAL_COST" | bc)
    REPORT_COUNT=$((REPORT_COUNT + 1))
    
    echo "  ✅ Generated invoice: ${INVOICE_FILE}"
    echo "  💰 Total: \$${TOTAL_COST}"
done

# Generate summary report
SUMMARY_FILE="${OUTPUT_DIR}/${YEAR_MONTH}/SUMMARY.txt"
cat > "$SUMMARY_FILE" <<EOF
========================================
MONTHLY BILLING SUMMARY
========================================

Period:             ${YEAR_MONTH}
Generated:          $(date)

Total Customers:    ${REPORT_COUNT}
Total Revenue:      \$${TOTAL_REVENUE}

Reports Location:   ${OUTPUT_DIR}/${YEAR_MONTH}/

========================================
EOF

echo ""
echo "========================================="
echo "SUMMARY"
echo "========================================="
cat "$SUMMARY_FILE"
echo ""
echo "All reports saved to: ${OUTPUT_DIR}/${YEAR_MONTH}/"
echo ""

# Optional: Send email notification
# if command -v mail &> /dev/null; then
#     mail -s "Monthly Billing Report - ${YEAR_MONTH}" billing@company.com < "$SUMMARY_FILE"
# fi

# Optional: Upload to S3 or cloud storage
# if command -v aws &> /dev/null; then
#     aws s3 sync "${OUTPUT_DIR}/${YEAR_MONTH}/" "s3://billing-reports/${YEAR_MONTH}/"
# fi
