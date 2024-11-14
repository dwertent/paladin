FROM ubuntu:latest

# Install necessary dependencies
RUN apt-get update && \
    apt-get install -y curl sudo && \
    curl https://raw.githubusercontent.com/nektos/act/master/install.sh | sudo bash && \
    apt-get clean

# Copy the entrypoint script
COPY entrypoint.sh /entrypoint.sh

# Make the entrypoint executable
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]